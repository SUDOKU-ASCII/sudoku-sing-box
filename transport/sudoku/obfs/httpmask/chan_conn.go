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
package httpmask

import (
	"io"
	"net"
	"sync"
	"time"
)

// chanConn provides a net.Conn-like interface backed by internal channels.
//
// It is used by HTTP tunnel modes (stream/poll) to bridge HTTP request/response bodies to
// a byte-stream API while preserving CloseWrite semantics.
type chanConn struct {
	rxc    chan []byte
	closed chan struct{}

	writeCh chan []byte
	// writeClosed is closed by CloseWrite to stop accepting new payloads.
	// When closed, Write returns io.ErrClosedPipe, but Read is unaffected.
	writeClosed   chan struct{}
	writeGate     sync.RWMutex
	writeDone     chan struct{}
	writeDoneOnce sync.Once
	writeErr      error
	readEOF       chan struct{}
	readEOFOnce   sync.Once
	readMu        sync.Mutex

	mu          sync.Mutex
	readBuf     []byte // unread tail of readPayload
	readPayload []byte
	closeErr    error
	localAddr   net.Addr
	remoteAddr  net.Addr
}

const chanConnQueueDepth = 64

const (
	pullPayloadBytes = 32 * 1024
	// A payload at least this full is queued as the pooled buffer it was read into; smaller ones are copied out so a
	// full queue of tiny payloads does not pin 32KiB each.
	pullHandoverMinBytes = pullPayloadBytes / 2
)

var pullPayloadPool = sync.Pool{New: func() any { return new([pullPayloadBytes]byte) }}

func getPullPayload() *[pullPayloadBytes]byte { return pullPayloadPool.Get().(*[pullPayloadBytes]byte) }

func putPullPayload(buf *[pullPayloadBytes]byte) { pullPayloadPool.Put(buf) }

// recyclePayload returns a fully read payload to the pool when it is a whole pooled-size buffer; only the
// receiving conn references it by then.
func recyclePayload(p []byte) {
	if cap(p) == pullPayloadBytes {
		putPullPayload((*[pullPayloadBytes]byte)(p[:pullPayloadBytes]))
	}
}

func newChanConn() chanConn {
	return chanConn{
		rxc:         make(chan []byte, chanConnQueueDepth),
		closed:      make(chan struct{}),
		writeCh:     make(chan []byte, chanConnQueueDepth),
		writeClosed: make(chan struct{}),
		writeDone:   make(chan struct{}),
		readEOF:     make(chan struct{}),
		localAddr:   &net.TCPAddr{},
		remoteAddr:  &net.TCPAddr{},
	}
}

func (c *chanConn) CloseWrite() error {
	c.writeGate.Lock()
	if !isClosedPipeChan(c.writeClosed) {
		close(c.writeClosed)
	}
	c.writeGate.Unlock()

	select {
	case <-c.writeDone:
		return c.completedWriteErr()
	default:
	}
	select {
	case <-c.writeDone:
		return c.completedWriteErr()
	case <-c.closed:
		return c.closedErr()
	}
}

func (c *chanConn) completedWriteErr() error {
	c.mu.Lock()
	err := c.writeErr
	c.mu.Unlock()
	return err
}

func (c *chanConn) completeWrite(err error) {
	c.mu.Lock()
	if c.writeErr == nil {
		c.writeErr = err
	}
	c.mu.Unlock()
	c.writeDoneOnce.Do(func() { close(c.writeDone) })
}

func (c *chanConn) markReadEOF() {
	c.readEOFOnce.Do(func() { close(c.readEOF) })
}

func (c *chanConn) closeWithError(err error) error {
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return nil
	default:
		if err == nil {
			err = io.ErrClosedPipe
		}
		if c.closeErr == nil {
			c.closeErr = err
		}
		close(c.closed)
	}
	c.mu.Unlock()
	return nil
}

func (c *chanConn) closedErr() error {
	c.mu.Lock()
	err := c.closeErr
	c.mu.Unlock()
	if err == nil {
		return io.ErrClosedPipe
	}
	return err
}

func (c *chanConn) Read(b []byte) (n int, err error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if len(c.readBuf) == 0 {
		select {
		case p := <-c.rxc:
			c.startPayload(p)
		default:
		}
	}
	if len(c.readBuf) == 0 {
		select {
		case p := <-c.rxc:
			c.startPayload(p)
		case <-c.readEOF:
			select {
			case p := <-c.rxc:
				c.startPayload(p)
			default:
				return 0, io.EOF
			}
		case <-c.closed:
			return 0, c.closedErr()
		}
	}
	n = copy(b, c.readBuf)
	c.readBuf = c.readBuf[n:]
	if len(c.readBuf) == 0 {
		recyclePayload(c.readPayload)
		c.readPayload = nil
	}
	return n, nil
}

// startPayload makes p the payload being read, recycling the previous one.
func (c *chanConn) startPayload(p []byte) {
	recyclePayload(c.readPayload)
	c.readPayload = p
	c.readBuf = p
}

func (c *chanConn) Write(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}

	payload := make([]byte, len(b))
	copy(payload, b)
	c.writeGate.RLock()
	defer c.writeGate.RUnlock()
	select {
	case <-c.closed:
		return 0, c.closedErr()
	default:
	}
	select {
	case <-c.writeClosed:
		return 0, io.ErrClosedPipe
	default:
	}
	select {
	case c.writeCh <- payload:
		return len(b), nil
	case <-c.closed:
		return 0, c.closedErr()
	}
}

func (c *chanConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *chanConn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *chanConn) SetDeadline(time.Time) error      { return nil }
func (c *chanConn) SetReadDeadline(time.Time) error  { return nil }
func (c *chanConn) SetWriteDeadline(time.Time) error { return nil }
