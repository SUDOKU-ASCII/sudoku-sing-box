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

var benchConnSink net.Conn

type benchCodec struct {
	name   string
	mode   string
	packed bool
}

func (c benchCodec) wrap(raw net.Conn, table *Table, pMin, pMax int) net.Conn {
	if c.packed {
		return NewPackedConn(raw, table, pMin, pMax)
	}
	return NewConn(raw, table, pMin, pMax, false)
}

var benchCodecs = []benchCodec{
	{"pure-ascii", "prefer_ascii", false},
	{"pure-entropy", "prefer_entropy", false},
	{"packed-ascii", "prefer_ascii", true},
	{"packed-entropy", "prefer_entropy", true},
}

var benchPaddings = []struct {
	name       string
	pMin, pMax int
}{
	{"nopad", 0, 0},
	{"default", 5, 15},
	{"full", 100, 100},
}

// BenchmarkCodec measures one 32 KiB payload per op through each codec and
// padding level, with B/s counted in plaintext bytes.
func BenchmarkCodec(b *testing.B) {
	data := randomBytes(b, 32*1024)

	for _, codec := range benchCodecs {
		table := mustTable(b, "bench-"+codec.name, codec.mode)
		for _, pad := range benchPaddings {
			b.Run(codec.name+"/write/"+pad.name, func(b *testing.B) {
				conn := codec.wrap(discardConn{}, table, pad.pMin, pad.pMax)
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := conn.Write(data); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run(codec.name+"/read/"+pad.name, func(b *testing.B) {
				var wire bytes.Buffer
				writer := codec.wrap(writeOnlyConn{Writer: &wire}, table, pad.pMin, pad.pMax)
				if _, err := writer.Write(data); err != nil {
					b.Fatal(err)
				}
				if f, ok := writer.(interface{ Flush() error }); ok {
					if err := f.Flush(); err != nil {
						b.Fatal(err)
					}
				}
				reader := codec.wrap(&cyclicReadConn{data: wire.Bytes()}, table, pad.pMin, pad.pMax)
				out := make([]byte, len(data))
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := io.ReadFull(reader, out); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkCodecConnConstruction(b *testing.B) {
	table := mustTable(b, "bench-construction", "prefer_ascii")
	b.Run("pure", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchConnSink = NewConn(discardConn{}, table, 5, 15, false)
		}
	})
	b.Run("packed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchConnSink = NewPackedConn(discardConn{}, table, 5, 15)
		}
	})
}
