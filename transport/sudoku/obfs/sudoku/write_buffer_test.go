package sudoku

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

type encodedWriterFunc func([]byte) (int, error)

func (f encodedWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestEncodedWriteBoundariesAndErrors(t *testing.T) {
	payload := bytes.Repeat([]byte("encoded bytes"), 10000)
	writeErr := errors.New("transport failed")
	for _, fail := range []error{nil, writeErr, io.ErrShortWrite} {
		total := 0
		err := writeEncoded(encodedWriterFunc(func(p []byte) (int, error) {
			if len(p) > 32*1024 || &p[0] != &payload[total] {
				t.Fatal("encoded write exceeded its limit or copied the buffer")
			}
			if fail != nil && total > 40000 {
				if fail == io.ErrShortWrite {
					return 0, nil
				}
				return 0, fail
			}
			// Exercise successful short writes within and across chunk boundaries.
			n := min(1000, len(p))
			total += n
			return n, nil
		}), payload)
		if err != fail || (fail == nil && total != len(payload)) {
			t.Fatalf("encoded write = %d, %v; expected error %v", total, err, fail)
		}
	}
}

func TestCodecLargeWriteUsesBoundedBuffer(t *testing.T) {
	table := mustTable(t, "bounded-write", "prefer_ascii")
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
