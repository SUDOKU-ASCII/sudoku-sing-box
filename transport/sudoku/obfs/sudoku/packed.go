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
	reader *wireReader
	rec    handshakeRecorder

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
		pc.padPool = make([]byte, 0, len(table.layout.paddingPool))
		for _, b := range table.layout.paddingPool {
			if b != pc.padMarker {
				pc.padPool = append(pc.padPool, b)
			}
		}
	}
	if len(pc.padPool) == 0 {
		pc.padPool = append(pc.padPool, pc.padMarker)
	}
	if record {
		pc.rec.start()
	}
	return pc
}

// StopRecording ends handshake recording and drops what was recorded.
func (pc *PackedConn) StopRecording() {
	if pc != nil {
		pc.rec.stop()
	}
}

// GetBufferedAndRecorded returns every wire byte read so far that has not been
// stopped from recording, plus bytes buffered but not yet decoded.
func (pc *PackedConn) GetBufferedAndRecorded() []byte {
	if pc == nil {
		return nil
	}
	return pc.rec.recordedWithBuffered(pc.reader.pending())
}

// appendByte feeds one plaintext byte into the 6-bit accumulator and emits every
// group it completes.
func (pc *PackedConn) appendByte(out []byte, b byte) []byte {
	pc.bitBuf = pc.bitBuf<<8 | uint64(b)
	pc.bitCount += 8
	for pc.bitCount >= 6 {
		pc.bitCount -= 6
		group := byte(pc.bitBuf >> pc.bitCount)
		pc.bitBuf &= 1<<pc.bitCount - 1
		out = appendPackedGroup(out, pc.table.layout, pc.rng, pc.paddingThreshold, pc.padPool, group)
	}
	return out
}

// appendForcedPadding adds one padding byte unconditionally.
func (pc *PackedConn) appendForcedPadding(out []byte) []byte {
	return append(out, pc.padPool[pc.rng.Intn(len(pc.padPool))])
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

	gap := 1 + pc.rng.Intn(2)
	effective := 0
	for i := 0; i < limit; i++ {
		out = pc.appendByte(out, p[i])

		effective++
		if effective >= gap {
			out = pc.appendForcedPadding(out)
			effective = 0
			gap = 1 + pc.rng.Intn(2)
		}
	}

	return out, limit
}

func appendPackedGroup(out []byte, layout *byteLayout, rng *sudokuRand, paddingThreshold uint64, padPool []byte, group byte) []byte {
	out = maybeAppendPackedPadding(out, rng, paddingThreshold, padPool)
	return append(out, layout.encodeGroup[group&0x3F])
}

// maybeAppendPackedPadding appends one random padding byte with the
// connection's padding probability.
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
	out := growWriteBuf(pc.writeBuf, needed)
	defer func() { pc.writeBuf = releaseWriteBuf(out) }()
	layout := pc.table.layout
	rng := pc.rng
	paddingThreshold := pc.paddingThreshold
	padPool := pc.padPool

	out, i := pc.writeProtectedPrefix(out, p)
	n := len(p)

	// Align the protected prefix to a complete group boundary.
	for pc.bitCount > 0 && i < n {
		out = maybeAppendPackedPadding(out, rng, paddingThreshold, padPool)
		out = pc.appendByte(out, p[i])
		i++
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
		out = pc.appendByte(out, p[i])
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

		out = append(out, pc.table.layout.encodeGroup[group])
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
		pc.reader = &wireReader{src: pc.Conn}
	}

	outN := 0
	for {
		chunk, rErr := pc.reader.peek()
		if len(chunk) > 0 {
			var consumed int
			outN, consumed, rErr = pc.decode(p, chunk)
			pc.rec.add(chunk[:consumed])
			pc.reader.discard(consumed)
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
