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
)

var errMuxReceiveQueueFull = errors.New("mux receive queue full")

type muxSession struct {
	conn net.Conn

	writeMu      sync.Mutex
	writeHeader  [muxHeaderSize]byte
	writeBuffers [2][]byte

	streamsMu sync.Mutex
	streams   map[uint32]*muxStream
	nextID    uint32

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error

	lastWrite     atomic.Int64
	keepaliveOnce sync.Once
	pongRequests  chan struct{}

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
	s.lastWrite.Store(time.Now().UnixNano())
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
			st.closeNoSend(err)
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

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	header := s.writeHeader[:]
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:5], streamID)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(payload)))

	// AEAD can include the mux header in the payload record instead of
	// encrypting and sending an extra tiny record for every frame.
	s.writeBuffers[0], s.writeBuffers[1] = header, payload
	_, err := connutil.WriteBuffers(s.conn, s.writeBuffers[:])
	s.writeBuffers[0], s.writeBuffers[1] = nil, nil
	if err != nil {
		s.closeWithError(err)
		return err
	}
	s.lastWrite.Store(time.Now().UnixNano())
	return nil
}

func (s *muxSession) startKeepalive(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	s.keepaliveOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					// Empty DATA is understood by older peers. Accept and answer
					// PING from newer peers without requiring it from older ones.
					if time.Since(time.Unix(0, s.lastWrite.Load())) < interval {
						continue
					}
					if err := s.sendFrame(muxFrameData, 0, nil); err != nil {
						return
					}
				case <-s.closed:
					return
				}
			}
		}()
	})
}

func (s *muxSession) requestPong() {
	select {
	case s.pongRequests <- struct{}{}:
	case <-s.closed:
	default:
	}
}

func (s *muxSession) controlLoop() {
	// A blocked PONG write must not prevent the read loop draining peer data.
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
				st.closeNoSend(err)
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
			st.closeNoSend(errors.New(msg))
			s.removeStream(streamID)

		default:
			s.closeWithError(fmt.Errorf("unknown mux frame type: %d", frameType))
			return
		}
	}
}

type muxStream struct {
	session *muxSession
	id      uint32

	writeMu sync.Mutex
	readMu  sync.Mutex // Serializes Read and WriteTo without blocking enqueue/close.

	mu                sync.Mutex
	cond              *sync.Cond
	closed            bool
	localReadClosed   bool
	localWriteClosed  bool
	remoteWriteClosed bool
	closeErr          error
	head              *muxChunk
	tail              *muxChunk
	// queuedBytes includes the chunk temporarily owned by WriteTo, if any.
	queuedBytes int

	localAddr  net.Addr
	remoteAddr net.Addr
}

func newMuxStream(session *muxSession, id uint32) *muxStream {
	st := &muxStream{
		session:    session,
		id:         id,
		localAddr:  &net.TCPAddr{},
		remoteAddr: &net.TCPAddr{},
	}
	st.cond = sync.NewCond(&st.mu)
	return st
}

func (c *muxStream) closeNoSend(err error) {
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *muxStream) closeRemoteWrite() bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	c.remoteWriteClosed = true
	remove := c.localWriteClosed
	c.cond.Broadcast()
	c.mu.Unlock()
	return remove
}

func (c *muxStream) closedErr() error {
	c.mu.Lock()
	err := c.closedErrLocked()
	c.mu.Unlock()
	return err
}

func (c *muxStream) closedErrLocked() error {
	if c.closeErr == nil {
		return io.ErrClosedPipe
	}
	return c.closeErr
}

func (c *muxStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.waitReadableLocked(); err != nil {
		return 0, err
	}

	chunk := c.head
	n := copy(p, chunk.data[chunk.off:])
	chunk.off += n
	if chunk.off == len(chunk.data) {
		c.popChunkLocked().release()
	}
	c.queuedBytes -= n
	return n, nil
}

func (c *muxStream) waitReadableLocked() error {
	for c.head == nil && !c.closed && !c.localReadClosed && !c.remoteWriteClosed {
		c.cond.Wait()
	}
	if c.head == nil {
		switch {
		case c.closed:
			return c.closedErrLocked()
		case c.localReadClosed:
			return io.ErrClosedPipe
		case c.remoteWriteClosed:
			return io.EOF
		}
	}

	return nil
}

func (c *muxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.session.isClosed() {
		return 0, c.session.closedErr()
	}
	c.mu.Lock()
	closed := c.closed
	writeClosed := c.localWriteClosed
	c.mu.Unlock()
	if closed {
		return 0, c.closedErr()
	}
	if writeClosed {
		return 0, io.ErrClosedPipe
	}

	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > muxMaxDataPayload {
			chunk = p[:muxMaxDataPayload]
		}
		if err := c.session.sendFrame(muxFrameData, c.id, chunk); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (c *muxStream) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.localReadClosed = true
		c.discardQueueLocked()
		c.mu.Unlock()
		return nil
	}
	sendClose := !c.localWriteClosed
	c.closed = true
	c.localReadClosed = true
	c.localWriteClosed = true
	if c.closeErr == nil {
		c.closeErr = io.ErrClosedPipe
	}
	c.discardQueueLocked()
	c.cond.Broadcast()
	c.mu.Unlock()

	if c.session != nil {
		if sendClose {
			_ = c.session.sendFrame(muxFrameClose, c.id, nil)
		}
		c.session.removeStream(c.id)
	}
	return nil
}

func (c *muxStream) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.Lock()
	if c.closed || c.localWriteClosed {
		c.mu.Unlock()
		return nil
	}
	c.localWriteClosed = true
	remove := c.remoteWriteClosed || c.localReadClosed
	c.mu.Unlock()

	if c.session == nil {
		return nil
	}
	err := c.session.sendFrame(muxFrameClose, c.id, nil)
	if remove {
		c.session.removeStream(c.id)
	}
	return err
}

func (c *muxStream) CloseRead() error {
	c.mu.Lock()
	if c.localReadClosed {
		c.mu.Unlock()
		return nil
	}
	c.localReadClosed = true
	c.discardQueueLocked()
	remove := c.localWriteClosed
	c.cond.Broadcast()
	c.mu.Unlock()

	if remove && c.session != nil {
		c.session.removeStream(c.id)
	}
	return nil
}

func (c *muxStream) LocalAddr() net.Addr  { return c.localAddr }
func (c *muxStream) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *muxStream) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	_ = c.SetWriteDeadline(t)
	return nil
}
func (c *muxStream) SetReadDeadline(time.Time) error  { return nil }
func (c *muxStream) SetWriteDeadline(time.Time) error { return nil }

// enqueue takes ownership of chunk, including when the stream rejects it.
func (c *muxStream) enqueue(chunk *muxChunk) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.localReadClosed || c.remoteWriteClosed || len(chunk.data) == 0 {
		chunk.release()
		return nil
	}
	if c.queuedBytes+len(chunk.data) > muxMaxQueuedBytesPerStream {
		chunk.release()
		return errMuxReceiveQueueFull
	}
	c.queuedBytes += len(chunk.data)
	if c.tail == nil {
		c.head = chunk
	} else {
		c.tail.next = chunk
	}
	c.tail = chunk
	c.cond.Signal()
	return nil
}
