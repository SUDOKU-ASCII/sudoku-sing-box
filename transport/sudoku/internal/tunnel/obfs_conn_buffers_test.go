package tunnel

import (
	"bytes"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing-box/transport/sudoku/crypto"
)

type recordingWriteConn struct {
	net.Conn
	wire   bytes.Buffer
	writes int
}

func (c *recordingWriteConn) Write(p []byte) (int, error) {
	c.writes++
	return c.wire.Write(p)
}

func (c *recordingWriteConn) Read(p []byte) (int, error) { return c.wire.Read(p) }

func TestObfsMetaPreservesRecordWriteBuffers(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "chacha20-poly1305", "none"} {
		t.Run(method, func(t *testing.T) {
			raw := new(recordingWriteConn)
			key := make([]byte, 32)
			record, err := crypto.NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			wrapped := wrapConnWithObfsMeta(record, obfsUplinkPacked)
			buffers := net.Buffers{[]byte("header"), []byte("payload")}
			n, err := connutil.WriteBuffers(wrapped, buffers)
			if n != 13 || err != nil {
				t.Fatalf("write = %d, %v", n, err)
			}
			wantWrites := 1
			if method == "none" {
				wantWrites = 2
			}
			if raw.writes != wantWrites {
				t.Fatalf("underlying writes = %d; want %d", raw.writes, wantWrites)
			}
			reader, err := crypto.NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			out := make([]byte, 13)
			if _, err := io.ReadFull(reader, out); err != nil || string(out) != "headerpayload" {
				t.Fatalf("read = %q, %v", out, err)
			}
			if packed, ok := ConnUplinkPacked(wrapped); !ok || !packed {
				t.Fatal("metadata was lost")
			}
		})
	}
}
