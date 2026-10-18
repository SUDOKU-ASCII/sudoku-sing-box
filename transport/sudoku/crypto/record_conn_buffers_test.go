package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
)

func TestRecordConnWriteBuffersKeyUpdate(t *testing.T) {
	old := atomic.SwapInt64(&KeyUpdateAfterBytes, 64*1024)
	t.Cleanup(func() { atomic.StoreInt64(&KeyUpdateAfterBytes, old) })
	for _, method := range []string{"aes-128-gcm", "chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			key := make([]byte, 32)
			raw := &captureConn{}
			writer, err := NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			parts := net.Buffers{[]byte("prefix"), bytes.Repeat([]byte("rotating"), 32768), []byte("suffix")}
			want := bytes.Join(parts, nil)
			if _, err := writer.WriteBuffers(parts); err != nil {
				t.Fatal(err)
			}
			if writer.sendEpochUpdates < 2 {
				t.Fatal("test did not rotate keys")
			}
			reader, err := NewRecordConn(&replayConn{reader: bytes.NewReader(raw.Bytes())}, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(want))
			if _, err := io.ReadFull(reader, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("payload changed across key updates")
			}
		})
	}
}

func TestRecordConnDirectReadRejectsTampering(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			key := make([]byte, 32)
			raw := &captureConn{}
			writer, err := NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte("authenticated payload")); err != nil {
				t.Fatal(err)
			}
			raw.Bytes()[raw.Len()-1] ^= 1
			reader, err := NewRecordConn(&replayConn{reader: bytes.NewReader(raw.Bytes())}, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := reader.Read(make([]byte, 1024)); n != 0 || err == nil {
				t.Fatalf("tampered record returned %d, %v", n, err)
			}
			if reader.recvInitialized || reader.hasPendingPlainLocked() {
				t.Fatal("failed authentication advanced receive state")
			}
		})
	}
}

func TestRecordConnWriteBuffersMatchesWrite(t *testing.T) {
	for _, method := range []string{"none", "aes-128-gcm", "chacha20-poly1305"} {
		for _, size := range []int{0, 1, 1024, 65507, 65508, 2*65507 + 19} {
			t.Run(fmt.Sprintf("%s/%d", method, size), func(t *testing.T) {
				key := bytes.Repeat([]byte{42}, 32)
				payload := make([]byte, size)
				for i := range payload {
					payload[i] = byte(i * 17)
				}
				want := bytes.Clone(payload)
				var parts net.Buffers
				for off := 0; off < size; {
					n := min(size-off, 1+off%4096)
					parts = append(parts, nil, payload[off:off+n], []byte{})
					off += n
				}
				single, vector := &captureConn{}, &captureConn{}
				w1, err := NewRecordConn(single, method, key, key)
				if err != nil {
					t.Fatal(err)
				}
				w2, err := NewRecordConn(vector, method, key, key)
				if err != nil {
					t.Fatal(err)
				}
				w2.sendEpoch, w2.sendSeq = w1.sendEpoch, w1.sendSeq
				if _, err := w1.Write(payload); err != nil {
					t.Fatal(err)
				}
				if n, err := w2.WriteBuffers(parts); err != nil || n != int64(size) {
					t.Fatalf("WriteBuffers = %d, %v", n, err)
				}
				if !bytes.Equal(single.Bytes(), vector.Bytes()) {
					t.Fatal("wire format differs from contiguous Write")
				}
				if !bytes.Equal(bytes.Join(parts, nil), want) {
					t.Fatal("caller buffers were modified")
				}
				reader, err := NewRecordConn(&replayConn{reader: bytes.NewReader(vector.Bytes())}, method, key, key)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("round trip: %v", err)
				}
			})
		}
	}
}

func TestRecordConnReadDoesNotRetainCallerBuffer(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			key := make([]byte, 32)
			raw := &captureConn{}
			writer, err := NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("caller-owned"), 256)
			for range 3 {
				if _, err := writer.Write(payload); err != nil {
					t.Fatal(err)
				}
			}
			reader, err := NewRecordConn(&replayConn{reader: bytes.NewReader(raw.Bytes())}, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			first := make([]byte, len(payload))
			if _, err := io.ReadFull(reader, first); err != nil {
				t.Fatal(err)
			}
			// Force both pending-plaintext and direct-decrypt paths on later records.
			second := make([]byte, 2*len(payload))
			if _, err := io.ReadFull(reader, second[:7]); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(reader, second[7:]); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, payload) || !bytes.Equal(second, bytes.Repeat(payload, 2)) {
				t.Fatal("read buffers alias later records")
			}
		})
	}
}

type limitedWriteConn struct {
	captureConn
	limit     int
	remaining int
	err       error
}

func (c *limitedWriteConn) Write(p []byte) (int, error) {
	if c.remaining == 0 {
		return 0, c.err
	}
	n := min(len(p), c.limit, c.remaining)
	c.remaining -= n
	return c.captureConn.Write(p[:n])
}

func TestRecordConnWriteBuffersShortWrites(t *testing.T) {
	for _, method := range []string{"none", "aes-128-gcm", "chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			key := make([]byte, 32)
			raw := &limitedWriteConn{limit: 3, remaining: 10000}
			writer, err := NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			parts := net.Buffers{[]byte("header"), nil, []byte("payload")}
			if n, err := writer.WriteBuffers(parts); n != 13 || err != nil {
				t.Fatalf("short writes: %d, %v", n, err)
			}
			raw.remaining = 0
			if n, err := writer.WriteBuffers(parts); n != 0 || !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("zero write: %d, %v", n, err)
			}
			raw.err = net.ErrClosed
			if n, err := writer.WriteBuffers(parts); n != 0 || !errors.Is(err, net.ErrClosed) {
				t.Fatalf("failed write: %d, %v", n, err)
			}
		})
	}
}
