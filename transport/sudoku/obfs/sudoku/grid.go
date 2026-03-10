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

// Grid represents a 4x4 sudoku grid
type Grid [16]uint8

// GenerateAllGrids generates all valid 4x4 Sudoku grids
func GenerateAllGrids() []Grid {
	var grids []Grid
	var g Grid
	var backtrack func(int)

	backtrack = func(idx int) {
		if idx == 16 {
			grids = append(grids, g)
			return
		}
		row, col := idx/4, idx%4
		br, bc := (row/2)*2, (col/2)*2
		for num := uint8(1); num <= 4; num++ {
			valid := true
			for i := 0; i < 4; i++ {
				if g[row*4+i] == num || g[i*4+col] == num {
					valid = false
					break
				}
			}
			if valid {
				for r := 0; r < 2; r++ {
					for c := 0; c < 2; c++ {
						if g[(br+r)*4+(bc+c)] == num {
							valid = false
							break
						}
					}
				}
			}
			if valid {
				g[idx] = num
				backtrack(idx + 1)
				g[idx] = 0
			}
		}
	}
	backtrack(0)
	return grids
}
