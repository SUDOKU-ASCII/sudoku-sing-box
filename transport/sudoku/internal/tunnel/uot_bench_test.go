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
package tunnel

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
)

// cycleReader serves data over and over, so a benchmark can read frames forever.
type cycleReader struct {
	data []byte
	pos  int
}

func (c *cycleReader) Read(p []byte) (int, error) {
	n := copy(p, c.data[c.pos:])
	c.pos = (c.pos + n) % len(c.data)
	return n, nil
}

func uotBenchFrame(ap netip.AddrPort, payloadLen int) []byte {
	addr := protocol.AppendAddrPort(nil, ap)
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(addr)))
	frame = binary.BigEndian.AppendUint16(frame, uint16(payloadLen))
	frame = append(frame, addr...)
	return append(frame, make([]byte, payloadLen)...)
}

var uotBenchPayloads = []struct {
	name string
	size int
}{
	{"dns-100B", 100},
	{"quic-1200B", 1200},
}

func BenchmarkReadUoTDatagram(b *testing.B) {
	ap := netip.MustParseAddrPort("203.0.113.7:443")
	for _, tc := range uotBenchPayloads {
		frame := uotBenchFrame(ap, tc.size)
		b.Run("oneshot/"+tc.name, func(b *testing.B) {
			src := &cycleReader{data: frame}
			b.ReportAllocs()
			b.SetBytes(int64(tc.size))
			for i := 0; i < b.N; i++ {
				if _, _, err := ReadUoTDatagram(src); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("persistent/"+tc.name, func(b *testing.B) {
			r := NewUoTReader(&cycleReader{data: frame})
			b.ReportAllocs()
			b.SetBytes(int64(tc.size))
			for i := 0; i < b.N; i++ {
				if _, _, err := r.ReadDatagram(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// A datagram addressed to a domain: resolved per datagram before destinations were cached.
func BenchmarkUoTDomainDestination(b *testing.B) {
	addr, err := protocol.AppendAddress(nil, "localhost:5353")
	if err != nil {
		b.Fatal(err)
	}
	b.Run("resolve-every-datagram", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, host, err := parseUoTAddr(addr)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := net.ResolveUDPAddr("udp", host); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		dests := newUoTDestinations()
		now := time.Now()
		if _, err := dests.lookup(addr, now); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := dests.lookup(addr, now); err != nil {
				b.Fatal(err)
			}
		}
	})
}
