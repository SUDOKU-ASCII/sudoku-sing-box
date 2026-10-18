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
package sudoku

import (
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

type Conn struct {
	net.Conn
	table  *Table
	reader *wireReader
	rec    handshakeRecorder

	hintBuf   [3]uint8 // hint indexes of a group still being read
	hintCount int
	writeMu   sync.Mutex
	writeBuf  []byte

	rng              *sudokuRand
	paddingThreshold uint64
}

func (sc *Conn) CloseWrite() error {
	if sc == nil {
		return nil
	}
	return connutil.TryCloseWrite(sc.Conn)
}

func (sc *Conn) CloseRead() error {
	if sc == nil {
		return nil
	}
	return connutil.TryCloseRead(sc.Conn)
}

func NewConn(c net.Conn, table *Table, pMin, pMax int, record bool) *Conn {
	localRng := newSeededRand()

	sc := &Conn{
		Conn:             c,
		table:            table,
		rng:              localRng,
		paddingThreshold: pickPaddingThreshold(localRng, pMin, pMax),
	}
	if record {
		sc.rec.start()
	}
	return sc
}

// StopRecording ends handshake recording and drops what was recorded.
func (sc *Conn) StopRecording() {
	if sc != nil {
		sc.rec.stop()
	}
}

// GetBufferedAndRecorded returns every wire byte read so far that has not been
// stopped from recording, plus bytes buffered but not yet decoded.
func (sc *Conn) GetBufferedAndRecorded() []byte {
	if sc == nil {
		return nil
	}
	return sc.rec.recordedWithBuffered(sc.reader.pending())
}

func (sc *Conn) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if sc == nil || sc.Conn == nil || sc.table == nil || sc.table.layout == nil || sc.rng == nil {
		return 0, io.ErrClosedPipe
	}

	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()

	sc.writeBuf, n, err = writeSudokuPayload(sc.Conn, sc.writeBuf, sc.table, sc.rng, sc.paddingThreshold, p)
	return n, err
}

func (sc *Conn) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if sc == nil || sc.Conn == nil || sc.table == nil || sc.table.layout == nil {
		return 0, io.ErrClosedPipe
	}
	if sc.reader == nil {
		// Directional connections also construct write-only codecs.
		sc.reader = &wireReader{src: sc.Conn}
	}

	outN := 0
	for {
		chunk, rErr := sc.reader.peek()
		if len(chunk) > 0 {
			var consumed int
			outN, consumed, rErr = sc.decode(p, chunk)
			sc.rec.add(chunk[:consumed])
			sc.reader.discard(consumed)
		}

		if rErr != nil {
			return 0, rErr
		}
		if outN > 0 {
			return outN, nil
		}
	}
}
