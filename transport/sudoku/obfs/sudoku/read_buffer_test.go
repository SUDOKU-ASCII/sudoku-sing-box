package sudoku

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
)

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
	table := mustTable(t, "borrowed-read-buffer", "prefer_ascii")
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
