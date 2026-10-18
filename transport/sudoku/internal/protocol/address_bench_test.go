package protocol

import (
	"bytes"
	"testing"
)

var benchAddrs = []struct{ name, addr string }{
	{"domain", "www.example.com:443"},
	{"ipv4", "203.0.113.7:443"},
	{"ipv6", "[2001:db8::1]:443"},
}

func BenchmarkWriteAddress(b *testing.B) {
	for _, c := range benchAddrs {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			var buf bytes.Buffer
			for i := 0; i < b.N; i++ {
				buf.Reset()
				if err := WriteAddress(&buf, c.addr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReadAddress(b *testing.B) {
	for _, c := range benchAddrs {
		var wire bytes.Buffer
		if err := WriteAddress(&wire, c.addr); err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, _, err := ReadAddress(bytes.NewReader(wire.Bytes())); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAppendAddress(b *testing.B) {
	for _, c := range benchAddrs {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			buf := make([]byte, 0, MaxEncodedAddrLen)
			for i := 0; i < b.N; i++ {
				var err error
				if buf, err = AppendAddress(buf[:0], c.addr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDecodeAddress(b *testing.B) {
	for _, c := range benchAddrs {
		wire, err := AppendAddress(nil, c.addr)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := DecodeAddress(wire); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
