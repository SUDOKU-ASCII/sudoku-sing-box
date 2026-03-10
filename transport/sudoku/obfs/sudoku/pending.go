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

// drainPending copies buffered decoded bytes into p.
// It returns (n, true) when pending data existed, otherwise (0, false).
func drainPending(p []byte, pending *[]byte) (int, bool) {
	if pending == nil || len(*pending) == 0 {
		return 0, false
	}
	n := copy(p, *pending)
	if n == len(*pending) {
		*pending = (*pending)[:0]
		return n, true
	}
	remaining := len(*pending) - n
	copy(*pending, (*pending)[n:])
	*pending = (*pending)[:remaining]
	return n, true
}
