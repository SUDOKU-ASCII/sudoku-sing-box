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
	"bytes"
	"io"
	"net"
	"testing"
)

func FuzzConnRoundTrip(f *testing.F) {
	table := NewTable("fuzz-key", "prefer_entropy")
	f.Add([]byte("hello"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Keep per-case runtime bounded.
		if len(data) > 256 {
			data = data[:256]
		}

		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()

		writer := NewConn(c1, table, 0, 0, false)
		reader := NewConn(c2, table, 0, 0, false)

		writeErr := make(chan error, 1)
		go func() {
			_, err := writer.Write(data)
			_ = c1.Close()
			writeErr <- err
		}()

		got := make([]byte, len(data))
		if _, err := io.ReadFull(reader, got); err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if err := <-writeErr; err != nil {
			t.Fatalf("write failed: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("roundtrip mismatch")
		}
	})
}
