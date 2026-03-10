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

// CloseReader is implemented by conns that support half-close on the read side.
type CloseReader interface {
	CloseRead() error
}

// CloseWriter is implemented by conns that support half-close on the write side.
type CloseWriter interface {
	CloseWrite() error
}

type closer interface {
	Close() error
}

// TryCloseRead calls CloseRead when supported.
func TryCloseRead(target any) error {
	if target == nil {
		return nil
	}
	if cr, ok := target.(CloseReader); ok {
		return cr.CloseRead()
	}
	return nil
}

// TryCloseWrite calls CloseWrite when supported. If half-close isn't supported,
// it falls back to Close() when available to avoid deadlocks (e.g. WebSocket
// net.Conn wrappers without CloseWrite).
func TryCloseWrite(target any) error {
	if target == nil {
		return nil
	}
	if cw, ok := target.(CloseWriter); ok {
		return cw.CloseWrite()
	}
	if c, ok := target.(closer); ok {
		return c.Close()
	}
	return nil
}

// RunClosers executes closers in order and returns the first error.
func RunClosers(closers ...func() error) error {
	var firstErr error
	for _, fn := range closers {
		if fn == nil {
			continue
		}
		if err := fn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func AbsInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
