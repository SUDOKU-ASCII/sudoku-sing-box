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
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
	"strings"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/logx"
)

var (
	ErrInvalidSudokuMapMiss = errors.New("INVALID_SUDOKU_MAP_MISS")
)

type Table struct {
	IsASCII bool // Marks the current encoding mode

	layout   *byteLayout
	opposite *Table
	hint     uint32

	// puzzles[puzzleStart[b]:puzzleStart[b+1]] are the puzzles of plaintext byte
	// b, each its four hint bytes in grid-cell order, first hint lowest.
	puzzleStart [257]uint32
	puzzles     []uint32

	// hintSets[pos<<2|val] holds bit k when the k-th shuffled grid has val at
	// pos. Four hints identify a plaintext byte exactly when their sets share a
	// single grid, and that grid's rank is the byte.
	hintSets [128][5]uint64
}

// NewTable initializes the obfuscation tables with built-in layouts.
// Equivalent to calling NewTableWithCustom(key, mode, "").
func NewTable(key string, mode string) *Table {
	t, err := NewTableWithCustom(key, mode, "")
	if err != nil {
		logx.Errorf("Init", "Failed to build table: %v", err)
		return nil
	}
	return t
}

// NewTableWithCustom initializes the uplink/probe Sudoku table using either predefined
// or directional layouts. Directional modes such as "up_ascii_down_entropy" return the
// client->server table and internally attach the opposite direction table for runtime use.
// The customPattern must contain 8 characters with exactly 2 x, 2 p, and 4 v (case-insensitive).
func NewTableWithCustom(key string, mode string, customPattern string) (*Table, error) {
	asciiMode, err := ParseASCIIMode(mode)
	if err != nil {
		return nil, err
	}
	uplinkPattern := customPatternForToken(asciiMode.Uplink, customPattern)
	downlinkPattern := customPatternForToken(asciiMode.Downlink, customPattern)
	hint := tableHintFingerprint(key, asciiMode.Canonical(), uplinkPattern, downlinkPattern)

	uplink, err := newSingleDirectionTable(key, asciiMode.uplinkPreference(), uplinkPattern)
	if err != nil {
		return nil, err
	}
	uplink.hint = hint
	if asciiMode.Uplink == asciiMode.Downlink {
		uplink.opposite = uplink
		return uplink, nil
	}

	downlink, err := newSingleDirectionTable(key, asciiMode.downlinkPreference(), downlinkPattern)
	if err != nil {
		return nil, err
	}
	downlink.hint = hint
	uplink.opposite = downlink
	downlink.opposite = uplink
	return uplink, nil
}

func newSingleDirectionTable(key string, mode string, customPattern string) (*Table, error) {
	start := time.Now()

	layout, err := resolveLayout(mode, customPattern)
	if err != nil {
		return nil, err
	}

	t := &Table{
		IsASCII: layout.name == "ascii",
		layout:  layout,
		puzzles: make([]uint32, 0, 24<<10),
	}

	grids := shuffledGrids(key)
	for k, g := range grids {
		for pos, val := range g {
			t.hintSets[pos<<2|int(val-1)][k>>6] |= 1 << (k & 63)
		}
	}

	for byteVal := 0; byteVal < 256; byteVal++ {
		t.puzzleStart[byteVal] = uint32(len(t.puzzles))
		g := &grids[byteVal]
		for _, positions := range hintPositions {
			var hints [4]uint8
			for i, pos := range positions {
				hints[i] = pos<<2 | (g[pos] - 1)
			}
			if got, ok := t.decodeHints(hints[0], hints[1], hints[2], hints[3]); !ok || got != byte(byteVal) {
				continue
			}
			var puzzle uint32
			for i, pos := range positions {
				puzzle |= uint32(layout.hintByte(g[pos]-1, pos)) << (8 * i)
			}
			t.puzzles = append(t.puzzles, puzzle)
		}
	}
	t.puzzleStart[256] = uint32(len(t.puzzles))

	logx.Infof("Init", "Sudoku Tables initialized (%s) in %v", layout.name, time.Since(start))
	return t, nil
}

// decodeHints returns the plaintext byte whose puzzle is exactly these four
// hint indexes (pos<<2|val, or poisonHint).
func (t *Table) decodeHints(a, b, c, d uint8) (byte, bool) {
	sa, sb, sc, sd := &t.hintSets[a&127], &t.hintSets[b&127], &t.hintSets[c&127], &t.hintSets[d&127]
	r0 := sa[0] & sb[0] & sc[0] & sd[0]
	r1 := sa[1] & sb[1] & sc[1] & sd[1]
	r2 := sa[2] & sb[2] & sc[2] & sd[2]
	r3 := sa[3] & sb[3] & sc[3] & sd[3]
	r4 := sa[4] & sb[4] & sc[4] & sd[4]

	// Exactly one shared grid, and it must be one of the 256 that carry a byte.
	multi := r0&(r0-1) | r1&(r1-1) | r2&(r2-1) | r3&(r3-1) | r4
	var nonEmpty uint64
	word := 0
	if r1 != 0 {
		nonEmpty++
		word = 64
	}
	if r2 != 0 {
		nonEmpty++
		word = 128
	}
	if r3 != 0 {
		nonEmpty++
		word = 192
	}
	if r0 != 0 {
		nonEmpty++
	}
	rank := word + bits.TrailingZeros64(r0|r1|r2|r3)
	return byte(rank), multi|(nonEmpty^1) == 0
}

func customPatternForToken(token string, customPattern string) string {
	if token == asciiModeTokenEntropy {
		return customPattern
	}
	return ""
}

func (t *Table) OppositeDirection() *Table {
	if t == nil || t.opposite == nil {
		return t
	}
	return t.opposite
}

func (t *Table) Hint() uint32 {
	if t == nil {
		return 0
	}
	return t.hint
}

func tableHintFingerprint(key string, mode string, uplinkPattern string, downlinkPattern string) uint32 {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"sudoku-table-hint",
		key,
		mode,
		strings.ToLower(strings.TrimSpace(uplinkPattern)),
		strings.ToLower(strings.TrimSpace(downlinkPattern)),
	}, "\x00")))
	return binary.BigEndian.Uint32(sum[:4])
}
