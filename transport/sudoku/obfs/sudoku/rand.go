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
	crypto_rand "crypto/rand"
	"encoding/binary"
	"math/bits"
	"time"
)

// sudokuRand is a wyrand generator. Its state advances by a constant, so draws
// do not depend on each other's results and the encoder can overlap them.
type sudokuRand struct {
	state uint64
}

func newSeededRand() *sudokuRand {
	seed := time.Now().UnixNano()
	var seedBytes [8]byte
	if _, err := crypto_rand.Read(seedBytes[:]); err == nil {
		seed = int64(binary.BigEndian.Uint64(seedBytes[:]))
	}
	return newSudokuRand(seed)
}

func newSudokuRand(seed int64) *sudokuRand {
	return &sudokuRand{state: uint64(seed)}
}

// wyNext is the generator step as a pure function so hot loops can keep the
// state in a register.
func wyNext(state uint64) (next, out uint64) {
	state += 0xa0761d6478bd642f
	hi, lo := bits.Mul64(state, state^0xe7037ed1a0b428db)
	return state, hi ^ lo
}

func (r *sudokuRand) Uint64() uint64 {
	var out uint64
	r.state, out = wyNext(r.state)
	return out
}

func (r *sudokuRand) Uint32() uint32 {
	return uint32(r.Uint64() >> 32)
}

func (r *sudokuRand) Intn(n int) int {
	if n <= 1 {
		return 0
	}
	return fastIntnFromUint32(r.Uint32(), n)
}

func fastIntnFromUint32(u uint32, n int) int {
	return int((uint64(u) * uint64(n)) >> 32)
}
