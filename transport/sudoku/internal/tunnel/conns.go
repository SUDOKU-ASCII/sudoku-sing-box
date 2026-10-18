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
	"bufio"
	"bytes"
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

// bufferedConn wraps net.Conn and bufio.Reader
type bufferedConn struct {
	net.Conn
	r          *bufio.Reader
	recorder   *bytes.Buffer
	recordLock sync.Mutex
}

func (bc *bufferedConn) CloseWrite() error {
	if bc == nil {
		return nil
	}
	return connutil.TryCloseWrite(bc.Conn)
}

func (bc *bufferedConn) CloseRead() error {
	if bc == nil {
		return nil
	}
	return connutil.TryCloseRead(bc.Conn)
}

func (bc *bufferedConn) Read(p []byte) (n int, err error) {
	n, err = bc.r.Read(p)
	if n > 0 && bc.recorder != nil {
		bc.recordLock.Lock()
		bc.recorder.Write(p[:n])
		bc.recordLock.Unlock()
	}
	return n, err
}

// preBufferedConn for Split detection peek
type preBufferedConn struct {
	net.Conn
	buf []byte
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

// NewPreBufferedConn replays the provided bytes before reading from the underlying connection.
func NewPreBufferedConn(conn net.Conn, preRead []byte) net.Conn {
	return &preBufferedConn{Conn: conn, buf: preRead}
}

func (p *preBufferedConn) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	if p.Conn == nil {
		return 0, io.EOF
	}
	return p.Conn.Read(b)
}

// GetBufferedAndRecorded returns all data that has been consumed and buffered
func (bc *bufferedConn) GetBufferedAndRecorded() []byte {
	if bc == nil {
		return nil
	}

	bc.recordLock.Lock()
	defer bc.recordLock.Unlock()

	var recorded []byte
	if bc.recorder != nil {
		recorded = bc.recorder.Bytes()
	}

	// Also get any buffered data that hasn't been read yet
	buffered := bc.r.Buffered()
	if buffered > 0 {
		peeked, _ := bc.r.Peek(buffered)
		full := make([]byte, len(recorded)+len(peeked))
		copy(full, recorded)
		copy(full[len(recorded):], peeked)
		return full
	}
	return recorded
}

type recordedConn struct {
	net.Conn
	recorded []byte
}

func (rc *recordedConn) CloseWrite() error {
	if rc == nil {
		return nil
	}
	return connutil.TryCloseWrite(rc.Conn)
}

func (rc *recordedConn) CloseRead() error {
	if rc == nil {
		return nil
	}
	return connutil.TryCloseRead(rc.Conn)
}

func (rc *recordedConn) GetBufferedAndRecorded() []byte {
	return rc.recorded
}

type prefixedRecorderConn struct {
	net.Conn
	prefix []byte
}

func (pc *prefixedRecorderConn) CloseWrite() error {
	if pc == nil {
		return nil
	}
	return connutil.TryCloseWrite(pc.Conn)
}

func (pc *prefixedRecorderConn) CloseRead() error {
	if pc == nil {
		return nil
	}
	return connutil.TryCloseRead(pc.Conn)
}

func (pc *prefixedRecorderConn) GetBufferedAndRecorded() []byte {
	var rest []byte
	if r, ok := pc.Conn.(interface{ GetBufferedAndRecorded() []byte }); ok {
		rest = r.GetBufferedAndRecorded()
	}
	out := make([]byte, 0, len(pc.prefix)+len(rest))
	out = append(out, pc.prefix...)
	out = append(out, rest...)
	return out
}
