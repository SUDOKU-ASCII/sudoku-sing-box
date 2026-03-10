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

const hintPositionsCount = 1820 // 16 choose 4

var hintPositions = buildHintPositions()

type hintPart struct {
	val byte
	pos byte
}

func buildHintPositions() [][4]byte {
	positions := make([][4]byte, 0, hintPositionsCount)
	for a := 0; a < 13; a++ {
		for b := a + 1; b < 14; b++ {
			for c := b + 1; c < 15; c++ {
				for d := c + 1; d < 16; d++ {
					positions = append(positions, [4]byte{byte(a), byte(b), byte(c), byte(d)})
				}
			}
		}
	}
	return positions
}

func hasUniqueMatch(grids []Grid, parts [4]hintPart) bool {
	matchCount := 0
	for _, g := range grids {
		match := true
		for _, p := range parts {
			if g[p.pos] != p.val {
				match = false
				break
			}
		}
		if match {
			matchCount++
			if matchCount > 1 {
				return false
			}
		}
	}
	return matchCount == 1
}
