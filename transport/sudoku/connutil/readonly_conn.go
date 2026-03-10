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
package connutil

import (
	"bytes"
	"io"
	"net"
	"time"
)

// ReadOnlyConn wraps a bytes.Reader as a net.Conn for probing/parsing code paths.
// Writes always fail with io.ErrClosedPipe.
type ReadOnlyConn struct {
	*bytes.Reader
}

func (c *ReadOnlyConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (c *ReadOnlyConn) Close() error                     { return nil }
func (c *ReadOnlyConn) LocalAddr() net.Addr              { return nil }
func (c *ReadOnlyConn) RemoteAddr() net.Addr             { return nil }
func (c *ReadOnlyConn) SetDeadline(time.Time) error      { return nil }
func (c *ReadOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (c *ReadOnlyConn) SetWriteDeadline(time.Time) error { return nil }
