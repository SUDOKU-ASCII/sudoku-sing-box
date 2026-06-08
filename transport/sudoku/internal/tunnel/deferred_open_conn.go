/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package tunnel

import (
	"net"
	"sync"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const deferredOpenReadGrace = 50 * time.Millisecond

type deferredOpenConn struct {
	net.Conn

	mu      sync.Mutex
	open    []byte
	openCh  chan struct{}
	opened  bool
	openErr error
}

func newDeferredKIPOpenConn(conn net.Conn, destAddr string) (net.Conn, error) {
	open, err := encodeKIPOpenTCP(destAddr)
	if err != nil {
		return nil, err
	}
	return &deferredOpenConn{Conn: conn, open: open, openCh: make(chan struct{})}, nil
}

func (c *deferredOpenConn) ensureOpenLocked(firstPayload []byte) error {
	if c.opened {
		return c.openErr
	}
	c.opened = true
	defer close(c.openCh)
	payload := c.open
	if len(firstPayload) > 0 {
		payload = make([]byte, 0, len(c.open)+len(firstPayload))
		payload = append(payload, c.open...)
		payload = append(payload, firstPayload...)
	}
	c.openErr = connutil.WriteFull(c.Conn, payload)
	c.open = nil
	return c.openErr
}

func (c *deferredOpenConn) waitForFirstWrite() error {
	timer := time.NewTimer(deferredOpenReadGrace)
	defer timer.Stop()
	select {
	case <-c.openCh:
		c.mu.Lock()
		err := c.openErr
		c.mu.Unlock()
		return err
	case <-timer.C:
		return c.ensureOpen()
	}
}

func (c *deferredOpenConn) ensureOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensureOpenLocked(nil)
}

func (c *deferredOpenConn) Read(p []byte) (int, error) {
	if err := c.waitForFirstWrite(); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c *deferredOpenConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	c.mu.Lock()
	if !c.opened {
		err := c.ensureOpenLocked(p)
		c.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return len(p), nil
	}
	err := c.openErr
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func (c *deferredOpenConn) Close() error {
	c.mu.Lock()
	if !c.opened {
		c.opened = true
		c.openErr = net.ErrClosed
		c.open = nil
		close(c.openCh)
	}
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *deferredOpenConn) CloseWrite() error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	return connutil.TryCloseWrite(c.Conn)
}

func (c *deferredOpenConn) CloseRead() error {
	return connutil.TryCloseRead(c.Conn)
}
