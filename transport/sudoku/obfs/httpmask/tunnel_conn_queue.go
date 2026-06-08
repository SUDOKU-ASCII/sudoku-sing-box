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
	"os"
	"sync"
	"time"
)

// queuedConn provides a net.Conn-like interface backed by internal channels.
//
// It is used by HTTP tunnel modes (stream/poll) to bridge HTTP request/response bodies to
// a byte-stream API while preserving CloseWrite semantics.
type queuedConn struct {
	rxc    chan []byte
	closed chan struct{}

	writeCh chan []byte
	// writeClosed is closed by CloseWrite to stop accepting new payloads.
	// When closed, Write returns io.ErrClosedPipe, but Read is unaffected.
	writeClosed chan struct{}

	mu         sync.Mutex
	readBuf    []byte
	closeErr   error
	localAddr  net.Addr
	remoteAddr net.Addr

	deadlineOnce  sync.Once
	readDeadline  pipeDeadline
	writeDeadline pipeDeadline
}

const queuedConnPayloadQueueDepth = 64

func (c *queuedConn) initDeadlines() {
	c.deadlineOnce.Do(func() {
		c.readDeadline = makePipeDeadline()
		c.writeDeadline = makePipeDeadline()
	})
}

func (c *queuedConn) CloseWrite() error {
	if c == nil || c.writeClosed == nil {
		return nil
	}
	c.mu.Lock()
	if !isClosedPipeChan(c.writeClosed) {
		close(c.writeClosed)
	}
	c.mu.Unlock()
	return nil
}

func (c *queuedConn) closeWithError(err error) error {
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

func (c *queuedConn) closedErr() error {
	c.mu.Lock()
	err := c.closeErr
	c.mu.Unlock()
	if err == nil {
		return io.ErrClosedPipe
	}
	return err
}

func (c *queuedConn) Read(b []byte) (n int, err error) {
	c.initDeadlines()
	if len(c.readBuf) == 0 {
		select {
		case c.readBuf = <-c.rxc:
		case <-c.closed:
			return 0, c.closedErr()
		case <-c.readDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
	n = copy(b, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *queuedConn) Write(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}
	c.initDeadlines()
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return 0, c.closedErr()
	case <-c.writeDeadline.wait():
		c.mu.Unlock()
		return 0, os.ErrDeadlineExceeded
	default:
	}
	if c.writeClosed != nil {
		select {
		case <-c.writeClosed:
			c.mu.Unlock()
			return 0, io.ErrClosedPipe
		case <-c.writeDeadline.wait():
			c.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		default:
		}
	}
	c.mu.Unlock()

	payload := make([]byte, len(b))
	copy(payload, b)
	if c.writeClosed == nil {
		select {
		case c.writeCh <- payload:
			return len(b), nil
		case <-c.closed:
			return 0, c.closedErr()
		case <-c.writeDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
	select {
	case c.writeCh <- payload:
		return len(b), nil
	case <-c.closed:
		return 0, c.closedErr()
	case <-c.writeClosed:
		return 0, io.ErrClosedPipe
	case <-c.writeDeadline.wait():
		return 0, os.ErrDeadlineExceeded
	}
}

func (c *queuedConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *queuedConn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *queuedConn) SetDeadline(t time.Time) error {
	c.initDeadlines()
	c.readDeadline.set(t)
	c.writeDeadline.set(t)
	return nil
}

func (c *queuedConn) SetReadDeadline(t time.Time) error {
	c.initDeadlines()
	c.readDeadline.set(t)
	return nil
}

func (c *queuedConn) SetWriteDeadline(t time.Time) error {
	c.initDeadlines()
	c.writeDeadline.set(t)
	return nil
}
