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

import "fmt"

// TableSet is a small helper for managing multiple Sudoku tables (e.g. for per-connection rotation).
// It is intentionally decoupled from the tunnel/app layers.
type TableSet struct {
	Tables []*Table
}

// NewTableSet builds one or more tables from key/mode and a list of custom X/P/V patterns.
// If patterns is empty, it builds a single default table (customPattern="").
func NewTableSet(key string, mode string, patterns []string) (*TableSet, error) {
	if len(patterns) == 0 {
		t, err := NewTableWithCustom(key, mode, "")
		if err != nil {
			return nil, err
		}
		return &TableSet{Tables: []*Table{t}}, nil
	}

	tables := make([]*Table, 0, len(patterns))
	for i, pattern := range patterns {
		t, err := NewTableWithCustom(key, mode, pattern)
		if err != nil {
			return nil, fmt.Errorf("build table[%d] (%q): %w", i, pattern, err)
		}
		tables = append(tables, t)
	}
	return &TableSet{Tables: tables}, nil
}

func (ts *TableSet) Candidates() []*Table {
	if ts == nil {
		return nil
	}
	return ts.Tables
}
