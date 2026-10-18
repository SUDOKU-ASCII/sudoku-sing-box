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

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

type preBufferedConn struct {
	net.Conn
	buf      []byte
	recorded []byte
	rejected bool
}

func (p *preBufferedConn) CloseWrite() error {
	if p == nil {
		return nil
	}
	return connutil.TryCloseWrite(p.Conn)
}

func (p *preBufferedConn) CloseRead() error {
	if p == nil {
		return nil
	}
	return connutil.TryCloseRead(p.Conn)
}

func newPreBufferedConn(conn net.Conn, pre []byte) *preBufferedConn {
	cpy := make([]byte, len(pre))
	copy(cpy, pre)
	return &preBufferedConn{Conn: conn, buf: cpy, recorded: cpy}
}

func newRejectedPreBufferedConn(conn net.Conn, pre []byte) *preBufferedConn {
	c := newPreBufferedConn(conn, pre)
	c.rejected = true
	return c
}

func (p *preBufferedConn) IsHTTPMaskRejected() bool { return p.rejected }

func (p *preBufferedConn) GetBufferedAndRecorded() []byte {
	if len(p.recorded) == 0 {
		return nil
	}
	out := make([]byte, len(p.recorded))
	copy(out, p.recorded)
	return out
}

func (p *preBufferedConn) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

type bodyConn struct {
	net.Conn
	reader io.Reader
	writer io.WriteCloser
	tail   io.Writer
	flush  func() error
}

func (c *bodyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *bodyConn) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if c.flush != nil {
		_ = c.flush()
	}
	return n, err
}

func (c *bodyConn) CloseWrite() error {
	if c == nil {
		return nil
	}

	var firstErr error
	if c.writer != nil {
		if err := c.writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		// NewChunkedWriter does not write the final CRLF. Ensure a clean terminator.
		if c.tail != nil {
			_, _ = c.tail.Write([]byte("\r\n"))
		} else if c.Conn != nil {
			_, _ = c.Conn.Write([]byte("\r\n"))
		}
		if c.flush != nil {
			_ = c.flush()
		}
		c.writer = nil
	}

	if c.Conn != nil {
		if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
			if err := cw.CloseWrite(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (c *bodyConn) CloseRead() error {
	if c == nil || c.Conn == nil {
		return nil
	}
	if cr, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return cr.CloseRead()
	}
	return nil
}

func (c *bodyConn) Close() error {
	var firstErr error
	if c.writer != nil {
		if err := c.writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		// NewChunkedWriter does not write the final CRLF. Ensure a clean terminator.
		if c.tail != nil {
			_, _ = c.tail.Write([]byte("\r\n"))
		} else {
			_, _ = c.Conn.Write([]byte("\r\n"))
		}
		if c.flush != nil {
			_ = c.flush()
		}
	}
	if err := c.Conn.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
