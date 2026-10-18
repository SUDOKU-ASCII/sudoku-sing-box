/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const (
	muxFrameOpen  byte = 0x01
	muxFrameData  byte = 0x02
	muxFrameClose byte = 0x03
	muxFrameReset byte = 0x04
	muxFramePing  byte = 0x05
	muxFramePong  byte = 0x06
)

const (
	muxHeaderSize = 1 + 4 + 4
	// muxMaxQueuedBytesPerStream bounds unread payload retained by a single logical stream.
	// A stream that exceeds the limit is reset so it cannot block the shared demux loop.
	muxMaxQueuedBytesPerStream = 4 * 1024 * 1024
	muxMaxFrameSize            = 256 * 1024
	// Larger data frames materially reduce per-frame lock/copy overhead for
	// single-tunnel large downloads while still staying well below the hard cap.
	muxMaxDataPayload    = 128 * 1024
	muxKeepaliveInterval = 15 * time.Second

	// Frames with at most muxCoalesceMaxPayload bytes are queued and batched with the frames of
	// other writers; larger ones are written directly, as copying them would cost more than the
	// saved writes. A writer that finds muxCoalesceMaxPending bytes queued waits for its turn to write.
	muxCoalesceMaxPayload = 4 * 1024
	muxCoalesceMaxPending = 256 * 1024
)

var errMuxReceiveQueueFull = errors.New("mux receive queue full")

type muxSession struct {
	conn net.Conn

	// writeMu is held by whichever goroutine is writing to conn. Small frames wait in pending
	// (guarded by pendMu) so that one write can carry the frames of several streams.
	writeMu      sync.Mutex
	writeHeader  [muxHeaderSize]byte
	writeBuffers [2][]byte
	pendMu       sync.Mutex
	pending      []byte
	spare        []byte

	streamsMu sync.Mutex
	streams   map[uint32]*muxStream
	nextID    uint32

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error

	// wroteSinceTick is set by sendFrame and consumed by the idle keepalive tick.
	wroteSinceTick atomic.Bool
	lastPong       atomic.Int64
	keepaliveOnce  sync.Once
	pongRequests   chan struct{}

	onOpen func(stream *muxStream, payload []byte)
}

func newMuxSession(conn net.Conn, onOpen func(stream *muxStream, payload []byte)) *muxSession {
	s := &muxSession{
		conn:         conn,
		streams:      make(map[uint32]*muxStream),
		closed:       make(chan struct{}),
		onOpen:       onOpen,
		pongRequests: make(chan struct{}, 1),
	}
	s.lastPong.Store(time.Now().UnixNano())
	go s.readLoop()
	go s.controlLoop()
	return s
}

func (s *muxSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *muxSession) closedErr() error {
	s.streamsMu.Lock()
	err := s.closeErr
	s.streamsMu.Unlock()
	if err == nil {
		return io.ErrClosedPipe
	}
	return err
}

func (s *muxSession) closeWithError(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}
	s.closeOnce.Do(func() {
		s.streamsMu.Lock()
		if s.closeErr == nil {
			s.closeErr = err
		}
		streams := make([]*muxStream, 0, len(s.streams))
		for _, st := range s.streams {
			streams = append(streams, st)
		}
		s.streams = make(map[uint32]*muxStream)
		s.streamsMu.Unlock()

		for _, st := range streams {
			st.abort(err)
		}

		close(s.closed)
		_ = s.conn.Close()
	})
}

func (s *muxSession) registerStream(st *muxStream) {
	s.streamsMu.Lock()
	s.streams[st.id] = st
	s.streamsMu.Unlock()
}

func (s *muxSession) getStream(id uint32) *muxStream {
	s.streamsMu.Lock()
	st := s.streams[id]
	s.streamsMu.Unlock()
	return st
}

func (s *muxSession) removeStream(id uint32) {
	s.streamsMu.Lock()
	delete(s.streams, id)
	s.streamsMu.Unlock()
}

func (s *muxSession) nextStreamID() uint32 {
	s.streamsMu.Lock()
	s.nextID++
	id := s.nextID
	if id == 0 {
		s.nextID++
		id = s.nextID
	}
	s.streamsMu.Unlock()
	return id
}

func (s *muxSession) sendFrame(frameType byte, streamID uint32, payload []byte) error {
	if s.isClosed() {
		return s.closedErr()
	}
	if len(payload) > muxMaxFrameSize {
		return fmt.Errorf("mux payload too large: %d", len(payload))
	}

	var err error
	if len(payload) <= muxCoalesceMaxPayload {
		err = s.sendQueued(frameType, streamID, payload)
	} else {
		err = s.sendDirect(frameType, streamID, payload)
	}
	if err != nil {
		return err
	}
	// Load first: the store only happens once per keepalive interval, so the hot path never
	// writes the shared cache line.
	if !s.wroteSinceTick.Load() {
		s.wroteSinceTick.Store(true)
	}
	return nil
}

// sendQueued appends the frame to the pending batch, then writes the batch unless another
// goroutine is already writing, which picks the frame up when it finishes its current write.
func (s *muxSession) sendQueued(frameType byte, streamID uint32, payload []byte) error {
	s.pendMu.Lock()
	buf := append(s.pending, frameType)
	buf = binary.BigEndian.AppendUint32(buf, streamID)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(payload)))
	s.pending = append(buf, payload...)
	full := len(s.pending) >= muxCoalesceMaxPending
	s.pendMu.Unlock()
	return s.flushPending(full)
}

// flushPending writes the pending batch. Unless wait is set it gives up when another goroutine
// holds the write role; that goroutine rechecks the batch after each write, so no frame is stranded.
func (s *muxSession) flushPending(wait bool) error {
	for {
		if wait {
			s.writeMu.Lock()
		} else if !s.writeMu.TryLock() {
			return nil
		}
		err := s.writePendingLocked()
		s.writeMu.Unlock()
		if err != nil {
			return err
		}
		wait = false

		s.pendMu.Lock()
		empty := len(s.pending) == 0
		s.pendMu.Unlock()
		if empty {
			return nil
		}
	}
}

// writePendingLocked writes the pending batch with one write; writeMu must be held.
func (s *muxSession) writePendingLocked() error {
	s.pendMu.Lock()
	buf := s.pending
	s.pending = s.spare[:0]
	s.spare = nil
	s.pendMu.Unlock()
	if len(buf) == 0 {
		s.recycleBatch(buf)
		return nil
	}

	err := connutil.WriteFull(s.conn, buf)
	s.recycleBatch(buf)
	if err != nil {
		s.closeWithError(err)
	}
	return err
}

func (s *muxSession) recycleBatch(buf []byte) {
	if cap(buf) > 1<<20 {
		return
	}
	s.pendMu.Lock()
	s.spare = buf[:0]
	s.pendMu.Unlock()
}

// sendDirect writes a large frame behind any batch that is already pending, which keeps the
// frames of a stream in order.
func (s *muxSession) sendDirect(frameType byte, streamID uint32, payload []byte) error {
	s.writeMu.Lock()
	err := s.writePendingLocked()
	if err == nil {
		header := s.writeHeader[:]
		header[0] = frameType
		binary.BigEndian.PutUint32(header[1:5], streamID)
		binary.BigEndian.PutUint32(header[5:9], uint32(len(payload)))

		// AEAD can include the mux header in the payload record instead of
		// encrypting and sending an extra tiny record for every frame.
		s.writeBuffers[0], s.writeBuffers[1] = header, payload
		_, err = connutil.WriteBuffers(s.conn, s.writeBuffers[:])
		s.writeBuffers[0], s.writeBuffers[1] = nil, nil
		if err != nil {
			s.closeWithError(err)
		}
	}
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	// Frames queued while this one was being written still need a writer.
	_ = s.flushPending(false)
	return nil
}

// muxKeepaliveKind selects how a session proves to its peer (and to middleboxes) that it is alive.
type muxKeepaliveKind uint8

const (
	// muxKeepaliveIdle sends an empty data frame after a full interval without any write.
	muxKeepaliveIdle muxKeepaliveKind = iota
	// muxKeepalivePing sends a ping every interval and closes the session once no pong
	// has arrived for three intervals.
	muxKeepalivePing
)

func (s *muxSession) startKeepalive(interval time.Duration, kind muxKeepaliveKind) {
	if s == nil || interval <= 0 {
		return
	}
	s.keepaliveOnce.Do(func() {
		s.wroteSinceTick.Store(false)
		go s.keepaliveLoop(interval, kind)
	})
}

func (s *muxSession) keepaliveLoop(interval time.Duration, kind muxKeepaliveKind) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !s.keepaliveTick(interval, kind) {
				return
			}
		case <-s.closed:
			return
		}
	}
}

// keepaliveTick reports whether the session is still usable.
func (s *muxSession) keepaliveTick(interval time.Duration, kind muxKeepaliveKind) bool {
	if kind == muxKeepalivePing {
		if time.Since(time.Unix(0, s.lastPong.Load())) > 3*interval {
			s.closeWithError(errors.New("mux keepalive timeout"))
			return false
		}
		return s.sendFrame(muxFramePing, 0, nil) == nil
	}
	if s.wroteSinceTick.Swap(false) {
		return true
	}
	return s.sendFrame(muxFrameData, 0, nil) == nil
}

func (s *muxSession) requestPong() {
	select {
	case s.pongRequests <- struct{}{}:
	case <-s.closed:
	default:
	}
}

func (s *muxSession) controlLoop() {
	for {
		select {
		case <-s.pongRequests:
			_ = s.sendFrame(muxFramePong, 0, nil)
		case <-s.closed:
			return
		}
	}
}

func (s *muxSession) sendReset(streamID uint32, msg string) {
	// Best-effort: ignore errors (session is probably already failing).
	if msg == "" {
		msg = "reset"
	}
	_ = s.sendFrame(muxFrameReset, streamID, []byte(msg))
	_ = s.sendFrame(muxFrameClose, streamID, nil)
}

func (s *muxSession) readLoop() {
	var header [muxHeaderSize]byte
	for {
		if _, err := io.ReadFull(s.conn, header[:]); err != nil {
			s.closeWithError(err)
			return
		}
		frameType := header[0]
		streamID := binary.BigEndian.Uint32(header[1:5])
		payloadLen := binary.BigEndian.Uint32(header[5:9])
		if payloadLen > muxMaxFrameSize {
			s.closeWithError(fmt.Errorf("invalid mux frame length: %d", payloadLen))
			return
		}
		n := int(payloadLen)
		if frameType == muxFrameData {
			if n == 0 {
				continue
			}
			chunk := acquireMuxChunk(n)
			if _, err := io.ReadFull(s.conn, chunk.data); err != nil {
				chunk.release()
				s.closeWithError(err)
				return
			}
			st := s.getStream(streamID)
			if st == nil {
				chunk.release()
				continue
			}
			if err := st.enqueue(chunk); err != nil {
				st.abort(err)
				s.removeStream(streamID)
				// Sending a reset must not hold up unrelated streams.
				go s.sendReset(streamID, err.Error())
			}
			continue
		}

		var payload []byte
		if n > 0 {
			payload = make([]byte, n)
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				s.closeWithError(err)
				return
			}
		}

		switch frameType {
		case muxFramePing:
			if streamID != 0 || len(payload) != 0 {
				s.closeWithError(errors.New("invalid mux ping frame"))
				return
			}
			s.requestPong()

		case muxFramePong:
			if streamID != 0 || len(payload) != 0 {
				s.closeWithError(errors.New("invalid mux pong frame"))
				return
			}
			s.lastPong.Store(time.Now().UnixNano())

		case muxFrameOpen:
			if s.onOpen == nil {
				s.sendReset(streamID, "unexpected open")
				continue
			}
			if streamID == 0 {
				s.sendReset(streamID, "invalid stream id")
				continue
			}
			if existing := s.getStream(streamID); existing != nil {
				s.sendReset(streamID, "stream already exists")
				continue
			}
			st := newMuxStream(s, streamID)
			s.registerStream(st)
			// Avoid blocking the demux loop on dial/IO.
			go s.onOpen(st, payload)

		case muxFrameClose:
			st := s.getStream(streamID)
			if st == nil {
				continue
			}
			if st.closeRemoteWrite() {
				s.removeStream(streamID)
			}

		case muxFrameReset:
			st := s.getStream(streamID)
			if st == nil {
				continue
			}
			msg := strings.TrimSpace(string(payload))
			if msg == "" {
				msg = "reset"
			}
			st.abort(errors.New(msg))
			s.removeStream(streamID)

		default:
			s.closeWithError(fmt.Errorf("unknown mux frame type: %d", frameType))
			return
		}
	}
}
