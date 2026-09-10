package sudoku

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

type discardConn struct{}

func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)      { return len(p), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return nil }
func (discardConn) RemoteAddr() net.Addr             { return nil }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

type writeOnlyConn struct{ Writer io.Writer }

func (c writeOnlyConn) Read([]byte) (int, error)       { return 0, io.EOF }
func (c writeOnlyConn) Write(p []byte) (int, error)    { return c.Writer.Write(p) }
func (writeOnlyConn) Close() error                     { return nil }
func (writeOnlyConn) LocalAddr() net.Addr              { return nil }
func (writeOnlyConn) RemoteAddr() net.Addr             { return nil }
func (writeOnlyConn) SetDeadline(time.Time) error      { return nil }
func (writeOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (writeOnlyConn) SetWriteDeadline(time.Time) error { return nil }

type fragmentConn struct {
	discardConn
	data       []byte
	off, limit int
}

func (c *fragmentConn) Read(p []byte) (int, error) {
	n := copy(p[:min(len(p), c.limit)], c.data[c.off:])
	c.off += n
	if c.off == len(c.data) {
		return n, io.EOF
	}
	return n, nil
}

type recordingCodec interface {
	net.Conn
	GetBufferedAndRecorded() []byte
	StopRecording()
}

func TestCodecBorrowedReadBuffer(t *testing.T) {
	table := NewTable("borrowed-read-buffer", "prefer_ascii")
	for _, packed := range []bool{false, true} {
		for _, fragmentSize := range []int{1, 7, 32768} {
			t.Run(fmt.Sprintf("packed=%v/fragment=%d", packed, fragmentSize), func(t *testing.T) {
				var wire bytes.Buffer
				var writer net.Conn
				if packed {
					writer = NewPackedConn(writeOnlyConn{Writer: &wire}, table, 10, 10)
				} else {
					writer = NewConn(writeOnlyConn{Writer: &wire}, table, 10, 10, false)
				}
				payload := bytes.Repeat([]byte("borrowed buffers must not alias"), 1500)
				// Separate writes exercise packed residual-bit markers too.
				for off := 0; off < len(payload); {
					n := min(137, len(payload)-off)
					if _, err := writer.Write(payload[off : off+n]); err != nil {
						t.Fatal(err)
					}
					off += n
				}
				raw := &fragmentConn{data: wire.Bytes(), limit: fragmentSize}
				var reader recordingCodec
				if packed {
					reader = NewPackedConnWithRecord(raw, table, 10, 10, true)
				} else {
					reader = NewConn(raw, table, 10, 10, true)
				}
				got := make([]byte, len(payload))
				for off := 0; off < len(got); {
					// Mix tiny header reads with reads that span multiple raw buffers.
					n := min([]int{1, 2, 37, 32768}[off%4], len(got)-off)
					if _, err := io.ReadFull(reader, got[off:off+n]); err != nil {
						t.Fatal(err)
					}
					off += n
					if !bytes.Equal(reader.GetBufferedAndRecorded(), raw.data[:raw.off]) {
						t.Fatal("fallback recording lost or duplicated wire bytes")
					}
				}
				if !bytes.Equal(got, payload) {
					t.Fatal("decoded data changed across reads")
				}
				if n, err := reader.Read(make([]byte, 1)); n != 0 || err != io.EOF {
					t.Fatalf("end of stream = %d, %v", n, err)
				}
				reader.StopRecording()
				if len(reader.GetBufferedAndRecorded()) != 0 {
					t.Fatal("recording retained after StopRecording")
				}
			})
		}
	}
}

// These digests pin the pre-optimization wire stream, including padding,
// protected prefixes and residual-bit markers, with a fixed random seed.
func TestCodecWireCompatibility(t *testing.T) {
	goldens := map[string]string{
		"prefer_ascii/packed=false/padding=0":     "3d71c28ecd52a4bc85a96d1b33338e2fb7ef232ccdd2d637f20f265c5d1ce86b",
		"prefer_ascii/packed=false/padding=10":    "fd8447e168016cec7d2b6f8f9efe5b0de4e0f7192810ca0006f8cca4dcfae4e6",
		"prefer_ascii/packed=false/padding=100":   "d15ace0e54d1637beb45cdf588461e683cd9f83ca1867c1d6b95caa8ecf6b252",
		"prefer_ascii/packed=true/padding=0":      "da4b1867414d76bd02359cead3207929c883128dc4006866c5c89bab2c0c6d06",
		"prefer_ascii/packed=true/padding=10":     "3e7e10f299b91afe0af5016e7d205d9a7c9a45b14a23190bbe8161640eb29487",
		"prefer_ascii/packed=true/padding=100":    "a9a62c246cc4d1ce84818697adf479f302760e95ccc2f809ab181beefa64148e",
		"prefer_entropy/packed=false/padding=0":   "680b1507632a5aeba3c4b0ad438d0c67abf02e34a0e866c1b73f9f8ab8b606b5",
		"prefer_entropy/packed=false/padding=10":  "51676390cc94a8aad08701639e397f47f425b98f1833da0880c12525c3fe1677",
		"prefer_entropy/packed=false/padding=100": "5c7d687c6b5785b83024f1d64a53c6c3f0cf3cf4d9f17de511072196dd73df61",
		"prefer_entropy/packed=true/padding=0":    "d88a51e8bb5c7bbeac2bd1d4c2698078cf035001e63d209759708df6a3ba5d43",
		"prefer_entropy/packed=true/padding=10":   "d177e19abaa3ae6d0573ce9aca4385e731c0ba2eb751fce51f7f4ae9e6ee9ce6",
		"prefer_entropy/packed=true/padding=100":  "d7028cf1f39f15cfa90b48351a72ace24d4d173e3f0b04964203ea16f71e7cfa",
		"custom/packed=false/padding=0":           "1ed09fecfd26e440ded7b03b957d9956f4bbb9ab12ee628a639ff225b9994431",
		"custom/packed=false/padding=10":          "907c9bcf2294685ac92553476a93226903b92bba4dbfc71ab849ad031ae36148",
		"custom/packed=false/padding=100":         "bf3e6d5c9cb1eaf46312e9438822981169ac1be9d7efec313616185f79043d9e",
		"custom/packed=true/padding=0":            "86973270ed688d486b30468a1d0be4aec004d0e9200bf1dc564add61329f3eb2",
		"custom/packed=true/padding=10":           "d3ff98a0f568607c7799532cc6bed740472bc06e683710c3570a430e722ce6e5",
		"custom/packed=true/padding=100":          "7152d54386c1725b4e07180279ba520e7b6736fef7096afa8f0132b73c3416f0",
	}

	for _, mode := range []string{"prefer_ascii", "prefer_entropy", "custom"} {
		preference, pattern := mode, ""
		if mode == "custom" {
			preference, pattern = "prefer_entropy", "xvpvvpxv"
		}
		table, err := NewTableWithCustom("wire-compatibility", preference, pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, packed := range []bool{false, true} {
			for _, padding := range []int{0, 10, 100} {
				name := fmt.Sprintf("%s/packed=%v/padding=%d", mode, packed, padding)
				t.Run(name, func(t *testing.T) {
					var wire bytes.Buffer
					var writer net.Conn
					if packed {
						c := NewPackedConn(writeOnlyConn{Writer: &wire}, table, padding, padding)
						c.rng = newSudokuRand(123456)
						writer = c
					} else {
						c := NewConn(writeOnlyConn{Writer: &wire}, table, padding, padding, false)
						c.rng = newSudokuRand(123456)
						writer = c
					}
					var want []byte
					for _, size := range []int{0, 1, 2, 3, 13, 14, 15, 16, 4097, 65537} {
						p := make([]byte, size)
						for i := range p {
							p[i] = byte(i*31 + i>>8)
						}
						if _, err := writer.Write(p); err != nil {
							t.Fatal(err)
						}
						want = append(want, p...)
					}
					digest := fmt.Sprintf("%x", sha256.Sum256(wire.Bytes()))
					if digest != goldens[name] {
						t.Fatal("encoded bytes differ from the legacy wire sample")
					}
					var reader net.Conn
					raw := &fragmentConn{data: wire.Bytes(), limit: 127}
					if packed {
						reader = NewPackedConn(raw, table, padding, padding)
					} else {
						reader = NewConn(raw, table, padding, padding, false)
					}
					got, err := io.ReadAll(reader)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("wire decode mismatch: %v", err)
					}
				})
			}
		}
	}
}

type encodedWriterFunc func([]byte) (int, error)

func (f encodedWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestCodecLargeWriteUsesBoundedBuffer(t *testing.T) {
	table := NewTable("bounded-write", "prefer_ascii")
	payload := bytes.Repeat([]byte("large application writes"), 50000)
	for _, packed := range []bool{false, true} {
		for _, padding := range []int{0, 10, 99, 100} {
			t.Run(fmt.Sprintf("packed=%v/padding=%d", packed, padding), func(t *testing.T) {
				var wire bytes.Buffer
				raw := writeOnlyConn{Writer: encodedWriterFunc(func(p []byte) (int, error) {
					if len(p) > maxEncodedWriteSize {
						t.Fatalf("transport write is too large: %d", len(p))
					}
					// Short writes must not change the encoded stream.
					return wire.Write(p[:min(len(p), 17000)])
				})}
				var writer net.Conn
				if packed {
					writer = NewPackedConn(raw, table, padding, padding)
				} else {
					writer = NewConn(raw, table, padding, padding, false)
				}
				if n, err := writer.Write(payload); n != len(payload) || err != nil {
					t.Fatalf("Write = %d, %v", n, err)
				}
				input := &fragmentConn{data: wire.Bytes(), limit: 32768}
				var reader net.Conn
				var retained int
				if packed {
					retained = cap(writer.(*PackedConn).writeBuf)
					reader = NewPackedConn(input, table, padding, padding)
				} else {
					retained = cap(writer.(*Conn).writeBuf)
					reader = NewConn(input, table, padding, padding, false)
				}
				if retained > maxEncodedWriteSize {
					t.Fatalf("retained %d encoded bytes", retained)
				}
				got, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("large Write round trip failed: %v", err)
				}
			})
		}
	}
}

func FuzzPackedReadCompatibility(f *testing.F) {
	custom, err := newCustomLayout("xvpvvpxv")
	if err != nil {
		f.Fatal(err)
	}
	layouts := []*byteLayout{newASCIILayout(), newEntropyLayout(), custom}
	f.Add([]byte{0x40, 0x41, 0x42, 0x43, 0x3f, '\n', 0xff}, uint8(0), uint8(7))
	f.Add([]byte{0, 1, 2, 3, 0x80, 0x10, 0x6f}, uint8(1), uint8(1))
	f.Add([]byte{}, uint8(2), uint8(32))
	f.Fuzz(func(t *testing.T, wire []byte, mode, fragment uint8) {
		wire = wire[:min(len(wire), 4096)]
		layout := layouts[int(mode)%len(layouts)]
		want := decodePackedReference(layout, wire)
		raw := &fragmentConn{data: wire, limit: 1 + int(fragment)}
		reader := NewPackedConn(raw, &Table{layout: layout}, 0, 0)
		got, err := io.ReadAll(reader)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("packed decoder differs from scalar decoder: %v", err)
		}

		// Check the optimized encoder against that legacy decoder too, across
		// write boundaries, padding levels and every supported layout.
		var encoded bytes.Buffer
		padding := []int{0, 10, 100}[int(fragment)%3]
		table := &Table{layout: layout, PaddingPool: layout.paddingPool}
		writer := NewPackedConn(writeOnlyConn{Writer: &encoded}, table, padding, padding)
		writer.rng = newSudokuRand(1)
		split := len(wire) / 2
		for _, p := range [][]byte{wire[:split], wire[split:]} {
			if _, err := writer.Write(p); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(decodePackedReference(layout, encoded.Bytes()), wire) {
			t.Fatal("encoded data is incompatible with the legacy decoder")
		}
	})
}

// Independent scalar decoder preserving the original acceptance rules,
// including legacy ASCII aliases and markers between incomplete groups.
func decodePackedReference(layout *byteLayout, wire []byte) []byte {
	var out []byte
	var bits uint32
	count := 0
	for _, b := range wire {
		if !layout.hintTable[b] {
			if b == layout.padMarker {
				bits, count = 0, 0
			}
			continue
		}
		bits = bits<<6 | uint32(layout.decodeGroup[b])
		count += 6
		if count >= 8 {
			count -= 8
			out = append(out, byte(bits>>count))
		}
	}
	return out
}
