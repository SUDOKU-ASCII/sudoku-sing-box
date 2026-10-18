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

func TestPureWriter_NoPaddingIsExactlyFourXAllHints(t *testing.T) {
	table := mustTable(t, "downlink-no-interpacket", "prefer_ascii")

	var raw bytes.Buffer
	writer := newPureWriter(&raw, table, newSudokuRand(1), 0, 0)
	payload := bytes.Repeat([]byte("a"), 256)

	if n, err := writer.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("write = (%d, %v), want (%d, nil)", n, err, len(payload))
	}
	if got, want := raw.Len(), len(payload)*4; got != want {
		t.Fatalf("wire len = %d, want %d", got, want)
	}
	if extra := countNonHintBytes(raw.Bytes(), table); extra != 0 {
		t.Fatalf("unexpected non-hint bytes: %d", extra)
	}
}

// The server's downlink writer must be readable by the matching client codec,
// with the packed variant needing its Flush closer to deliver the tail bits.
func TestServerDownlinkWriter_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		pure    bool
		payload []byte
		reader  func(net.Conn, *Table) io.Reader
	}{
		{"pure", true, bytes.Repeat([]byte("compat-downlink-"), 8), func(c net.Conn, tb *Table) io.Reader {
			return NewConn(c, tb, 0, 0, false)
		}},
		{"packed", false, bytes.Repeat([]byte("packed-downlink-compat"), 96), func(c net.Conn, tb *Table) io.Reader {
			return NewPackedConn(c, tb, 0, 0)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := mustTable(t, "downlink-roundtrip-"+tc.name, "prefer_entropy")
			c1, c2 := net.Pipe()
			defer c1.Close()
			defer c2.Close()

			writer, closers := NewServerDownlinkWriter(c1, table, 0, 0, tc.pure)
			writeErr := make(chan error, 1)
			go func() {
				_, err := writer.Write(tc.payload)
				for _, closeFn := range closers {
					if cerr := closeFn(); err == nil {
						err = cerr
					}
				}
				_ = c1.Close()
				writeErr <- err
			}()

			got, err := io.ReadAll(tc.reader(c2, table))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := <-writeErr; err != nil {
				t.Fatalf("write: %v", err)
			}
			if !bytes.Equal(got, tc.payload) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestPackedConn_SmallReadDoesNotOverDecode(t *testing.T) {
	table := mustTable(t, "packed-small-read", "prefer_ascii")

	mock := &memConn{}
	writer := NewPackedConn(mock, table, 0, 0)
	payload := bytes.Repeat([]byte("x"), 63*1024)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := NewPackedConn(&memConn{readBuf: mock.writeBuf}, table, 0, 0)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatalf("small read: %v", err)
	}

	if len(reader.reader.pending()) == 0 {
		t.Fatal("small read consumed the entire wire buffer")
	}
}

func countNonHintBytes(data []byte, table *Table) int {
	count := 0
	for _, b := range data {
		if !isHint(table.layout, b) {
			count++
		}
	}
	return count
}
