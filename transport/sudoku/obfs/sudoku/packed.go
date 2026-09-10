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
	"bufio"
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const (
	packedProtectedPrefixBytes = 14
)

// PackedConn is a bandwidth-optimized downlink codec.
//
// It encodes ciphertext bits into 6-bit groups, then maps them to Sudoku "hint" bytes with optional padding
// to keep the traffic profile consistent with the classic Sudoku codec:
//   - Write: batch complete 3-byte blocks into 4 groups, with the same padding probability model as Conn
//   - Read: decode groups back to bytes, avoiding slice aliasing leaks
type PackedConn struct {
	net.Conn
	table  *Table
	reader *bufio.Reader

	recorder   *bytes.Buffer
	recording  atomic.Bool
	recordLock sync.Mutex

	// Write buffer and state
	writeMu  sync.Mutex
	writeBuf []byte
	bitBuf   uint64 // pending bits (MSB-first)
	bitCount int    // number of valid pending bits in bitBuf

	// Read state
	readBitBuf uint64 // pending bits (MSB-first)
	readBits   int    // number of valid pending bits in readBitBuf

	// RNG and padding control — uses integer-threshold random, consistent with Conn
	rng              *sudokuRand
	paddingThreshold uint64 // Same probability model as Conn
	padMarker        byte
	padPool          []byte
}

func (pc *PackedConn) CloseWrite() error {
	if pc == nil {
		return nil
	}
	return connutil.TryCloseWrite(pc.Conn)
}

func (pc *PackedConn) CloseRead() error {
	if pc == nil {
		return nil
	}
	return connutil.TryCloseRead(pc.Conn)
}

func NewPackedConn(c net.Conn, table *Table, pMin, pMax int) *PackedConn {
	return NewPackedConnWithRecord(c, table, pMin, pMax, false)
}

func NewPackedConnWithRecord(c net.Conn, table *Table, pMin, pMax int, record bool) *PackedConn {
	localRng := newSeededRand()

	pc := &PackedConn{
		Conn:             c,
		table:            table,
		rng:              localRng,
		paddingThreshold: pickPaddingThreshold(localRng, pMin, pMax),
	}

	if table != nil && table.layout != nil {
		pc.padMarker = table.layout.padMarker
		pc.padPool = make([]byte, 0, len(table.PaddingPool))
		for _, b := range table.PaddingPool {
			if b != pc.padMarker {
				pc.padPool = append(pc.padPool, b)
			}
		}
	}
	if len(pc.padPool) == 0 {
		pc.padPool = append(pc.padPool, pc.padMarker)
	}
	if record {
		pc.recorder = new(bytes.Buffer)
		pc.recording.Store(true)
	}
	return pc
}

func (pc *PackedConn) StopRecording() {
	if pc == nil {
		return
	}
	pc.recordLock.Lock()
	pc.recording.Store(false)
	pc.recorder = nil
	pc.recordLock.Unlock()
}

func (pc *PackedConn) GetBufferedAndRecorded() []byte {
	if pc == nil {
		return nil
	}

	pc.recordLock.Lock()
	defer pc.recordLock.Unlock()

	var recorded []byte
	if pc.recorder != nil {
		recorded = pc.recorder.Bytes()
	}
	if pc.reader == nil {
		return recorded
	}

	buffered := pc.reader.Buffered()
	if buffered > 0 {
		peeked, _ := pc.reader.Peek(buffered)
		full := make([]byte, len(recorded)+len(peeked))
		copy(full, recorded)
		copy(full[len(recorded):], peeked)
		return full
	}
	return recorded
}

func (pc *PackedConn) appendForcedPadding(out []byte) []byte {
	return append(out, pc.getPaddingByte())
}

func (pc *PackedConn) nextProtectedPrefixGap() int {
	return 1 + pc.rng.Intn(2)
}

func (pc *PackedConn) writeProtectedPrefix(out []byte, p []byte) ([]byte, int) {
	if len(p) == 0 {
		return out, 0
	}

	limit := len(p)
	if limit > packedProtectedPrefixBytes {
		limit = packedProtectedPrefixBytes
	}

	for padCount := 0; padCount < 1+pc.rng.Intn(2); padCount++ {
		out = pc.appendForcedPadding(out)
	}

	gap := pc.nextProtectedPrefixGap()
	effective := 0
	for i := 0; i < limit; i++ {
		pc.bitBuf = (pc.bitBuf << 8) | uint64(p[i])
		pc.bitCount += 8
		for pc.bitCount >= 6 {
			pc.bitCount -= 6
			group := byte(pc.bitBuf >> pc.bitCount)
			if pc.bitCount == 0 {
				pc.bitBuf = 0
			} else {
				pc.bitBuf &= (1 << pc.bitCount) - 1
			}
			out = appendPackedGroup(out, pc.table.layout, pc.rng, pc.paddingThreshold, pc.padPool, group)
		}

		effective++
		if effective >= gap {
			out = pc.appendForcedPadding(out)
			effective = 0
			gap = pc.nextProtectedPrefixGap()
		}
	}

	return out, limit
}

func appendPackedGroup(out []byte, layout *byteLayout, rng *sudokuRand, paddingThreshold uint64, padPool []byte, group byte) []byte {
	if paddingThreshold != 0 {
		u := rng.Uint32()
		if uint64(u) < paddingThreshold {
			out = append(out, padPool[fastIntnFromUint32(rng.Uint32(), len(padPool))])
		}
	}
	return append(out, layout.encodeGroup[group&0x3F])
}

func maybeAppendPackedPadding(out []byte, rng *sudokuRand, paddingThreshold uint64, padPool []byte) []byte {
	if paddingThreshold != 0 {
		u := rng.Uint32()
		if uint64(u) < paddingThreshold {
			out = append(out, padPool[fastIntnFromUint32(rng.Uint32(), len(padPool))])
		}
	}
	return out
}

// Write encodes bytes into 6-bit groups and writes the corresponding hint bytes.
func (pc *PackedConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if pc == nil || pc.Conn == nil || pc.table == nil || pc.table.layout == nil || pc.rng == nil || len(pc.padPool) == 0 {
		return 0, io.ErrClosedPipe
	}

	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()

	// Bound storage independently of the caller's Write size, including padding.
	needed := min(min(len(p), maxEncodedWriteSize)*3+64, maxEncodedWriteSize)
	if cap(pc.writeBuf) < needed {
		pc.writeBuf = make([]byte, 0, needed)
	}
	out := pc.writeBuf[:0]
	defer func() { pc.writeBuf = out[:0] }()
	layout := pc.table.layout
	rng := pc.rng
	paddingThreshold := pc.paddingThreshold
	padPool := pc.padPool

	out, i := pc.writeProtectedPrefix(out, p)
	n := len(p)

	// Align the protected prefix to a complete group boundary.
	for pc.bitCount > 0 && i < n {
		out = maybeAppendPackedPadding(out, rng, paddingThreshold, padPool)
		b := p[i]
		i++
		pc.bitBuf = (pc.bitBuf << 8) | uint64(b)
		pc.bitCount += 8
		for pc.bitCount >= 6 {
			pc.bitCount -= 6
			group := byte(pc.bitBuf >> pc.bitCount)
			if pc.bitCount == 0 {
				pc.bitBuf = 0
			} else {
				pc.bitBuf &= (1 << pc.bitCount) - 1
			}
			out = appendPackedGroup(out, layout, rng, paddingThreshold, padPool, group)
		}
	}

	// Encode complete blocks in one batch. Prefix and tail handling retain the
	// same random draws and framing as the original per-group encoder.
	end := i + (n-i)/3*3
	blockSize := 4
	if paddingThreshold != 0 {
		blockSize = 8
	}
	written := 0
	for i < end {
		// Reserve room for the final two bytes, residual marker and trailing pad.
		count := min((cap(out)-len(out)-8)/blockSize*3, end-i)
		if count == 0 {
			if err := writeEncoded(pc.Conn, out); err != nil {
				return written, err
			}
			written = i
			out = out[:0]
			continue
		}
		out = appendPackedBlocks(out, p[i:i+count], layout, rng, paddingThreshold, padPool)
		i += count
	}

	// Handle the remaining 1 or 2 bytes.
	for ; i < n; i++ {
		b := p[i]
		pc.bitBuf = (pc.bitBuf << 8) | uint64(b)
		pc.bitCount += 8
		for pc.bitCount >= 6 {
			pc.bitCount -= 6
			group := byte(pc.bitBuf >> pc.bitCount)
			if pc.bitCount == 0 {
				pc.bitBuf = 0
			} else {
				pc.bitBuf &= (1 << pc.bitCount) - 1
			}
			out = appendPackedGroup(out, layout, rng, paddingThreshold, padPool, group)
		}
	}

	// Flush residual bits.
	if pc.bitCount > 0 {
		group := byte(pc.bitBuf << (6 - pc.bitCount))
		pc.bitBuf = 0
		pc.bitCount = 0
		out = appendPackedGroup(out, layout, rng, paddingThreshold, padPool, group)
		out = append(out, pc.padMarker)
	}

	// Possibly append trailing padding
	out = maybeAppendPackedPadding(out, rng, paddingThreshold, padPool)

	if err := writeEncoded(pc.Conn, out); err != nil {
		return written, err
	}
	return len(p), nil
}

// Flush writes any residual bits left by partial writes.
func (pc *PackedConn) Flush() error {
	if pc == nil || pc.Conn == nil || pc.table == nil || pc.table.layout == nil || pc.rng == nil || len(pc.padPool) == 0 {
		return io.ErrClosedPipe
	}

	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()

	out := pc.writeBuf[:0]
	if pc.bitCount > 0 {
		group := byte(pc.bitBuf << (6 - pc.bitCount))
		pc.bitBuf = 0
		pc.bitCount = 0

		out = append(out, pc.table.layout.groupByte(group&0x3F))
		out = append(out, pc.padMarker)
	}

	// Possibly append trailing padding
	out = maybeAppendPackedPadding(out, pc.rng, pc.paddingThreshold, pc.padPool)

	if len(out) > 0 {
		pc.writeBuf = out[:0]
		return writeEncoded(pc.Conn, out)
	}
	return nil
}

// Read decodes hint bytes back into the original byte stream.
func (pc *PackedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if pc == nil || pc.Conn == nil || pc.table == nil || pc.table.layout == nil {
		return 0, io.ErrClosedPipe
	}

	if pc.reader == nil {
		pc.reader = bufio.NewReaderSize(pc.Conn, PackedIOBufferSize)
	}

	outN := 0
	for {
		chunk, rErr := peekBufferedChunk(pc.reader)
		if len(chunk) > 0 {
			var consumed int
			outN, consumed, rErr = pc.decode(p, chunk)
			if pc.recording.Load() {
				pc.recordLock.Lock()
				if pc.recording.Load() && pc.recorder != nil {
					pc.recorder.Write(chunk[:consumed])
				}
				pc.recordLock.Unlock()
			}
			_, _ = pc.reader.Discard(consumed)
		}

		if rErr != nil {
			if rErr == io.EOF {
				pc.readBitBuf = 0
				pc.readBits = 0
			}
			return 0, rErr
		}

		if outN > 0 {
			return outN, nil
		}
	}
}

// getPaddingByte picks a random padding byte from the pool.
func (pc *PackedConn) getPaddingByte() byte {
	return pc.padPool[pc.rng.Intn(len(pc.padPool))]
}
