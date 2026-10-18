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
	"io"
	"net"
	"sync"
	"time"
)

type muxStream struct {
	session *muxSession
	id      uint32

	writeMu sync.Mutex
	readMu  sync.Mutex // Serializes Read and WriteTo without blocking enqueue/close.

	mu                sync.Mutex
	cond              sync.Cond // cond.L is &mu.
	closed            bool
	localReadClosed   bool
	localWriteClosed  bool
	remoteWriteClosed bool
	closeErr          error
	head              *muxChunk
	tail              *muxChunk
	// queuedBytes includes the chunk temporarily owned by WriteTo, if any.
	queuedBytes int
}

// muxStreamAddr is the placeholder address every stream reports; streams have no network
// address of their own, and sharing one value saves two allocations per stream.
var muxStreamAddr net.Addr = &net.TCPAddr{}

func newMuxStream(session *muxSession, id uint32) *muxStream {
	st := &muxStream{session: session, id: id}
	st.cond.L = &st.mu
	return st
}

// abort closes the stream locally and wakes blocked readers and writers; it sends no frame
// to the peer, so it is for streams the peer already ended or a dead session.
func (c *muxStream) abort(err error) {
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
	// A remote CLOSE only ends the remote-to-local direction. Keep the
	// stream registered while the local write side is still usable; removing
	// it here would silently discard a valid response after CloseRead.
	remove := c.localWriteClosed && c.remoteWriteClosed
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

func (c *muxStream) LocalAddr() net.Addr  { return muxStreamAddr }
func (c *muxStream) RemoteAddr() net.Addr { return muxStreamAddr }

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
