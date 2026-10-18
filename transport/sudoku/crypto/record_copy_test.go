package crypto

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type recordWriterFunc func([]byte) (int, error)

func (f recordWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestRecordWriteToPreservesSuffixAndKeyUpdates(t *testing.T) {
	old := atomic.SwapInt64(&KeyUpdateAfterBytes, 64*1024)
	t.Cleanup(func() { atomic.StoreInt64(&KeyUpdateAfterBytes, old) })
	for _, method := range []string{"aes-128-gcm", "chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			key := make([]byte, 32)
			raw := new(captureConn)
			writer, err := NewRecordConn(raw, method, key, key)
			if err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("authenticated stream"), 16000)
			if _, err := writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			for _, result := range []struct {
				n   int
				err error
			}{{0, nil}, {7, nil}, {-1, nil}, {1 << 20, nil}, {7, net.ErrClosed}} {
				reader, err := NewRecordConn(&replayConn{reader: bytes.NewReader(raw.Bytes())}, method, key, key)
				if err != nil {
					t.Fatal(err)
				}
				// A header-sized Read must leave the rest available to WriteTo.
				var prefix [9]byte
				if _, err := io.ReadFull(reader, prefix[:]); err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				got.Write(prefix[:])
				n, err := reader.WriteTo(recordWriterFunc(func(p []byte) (int, error) {
					if &p[0] != &reader.readPlain[reader.readOff] {
						t.Fatal("WriteTo copied the pending record")
					}
					if result.n >= 0 && result.n <= len(p) {
						got.Write(p[:result.n])
					}
					return result.n, result.err
				}))
				wantN := max(0, result.n)
				if wantN > 65507-9 {
					wantN = 0
				}
				wantErr := result.err
				if wantErr == nil {
					wantErr = io.ErrShortWrite
				}
				if n != int64(wantN) || !errors.Is(err, wantErr) {
					t.Fatalf("short WriteTo = %d, %v; want %d, %v", n, err, wantN, wantErr)
				}
				if _, err := io.Copy(&got, reader); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Bytes(), payload) {
					t.Fatal("short write lost or duplicated plaintext across key updates")
				}
			}
		})
	}
}

func TestRecordWriteToRejectsTampering(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "chacha20-poly1305"} {
		key := make([]byte, 32)
		raw := new(captureConn)
		writer, err := NewRecordConn(raw, method, key, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("must authenticate before forwarding")); err != nil {
			t.Fatal(err)
		}
		raw.Bytes()[raw.Len()-1] ^= 1
		reader, err := NewRecordConn(&replayConn{reader: bytes.NewReader(raw.Bytes())}, method, key, key)
		if err != nil {
			t.Fatal(err)
		}
		n, err := reader.WriteTo(recordWriterFunc(func([]byte) (int, error) {
			t.Fatal("unauthenticated plaintext reached the destination")
			return 0, nil
		}))
		if n != 0 || err == nil || reader.recvInitialized || reader.hasPendingPlainLocked() {
			t.Fatalf("tampered WriteTo = %d, %v", n, err)
		}
	}
}

func TestRecordWriteToCloseUnblocksRead(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	key := make([]byte, 32)
	reader, err := NewRecordConn(a, "aes-128-gcm", key, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	done := make(chan error, 1)
	go func() { _, err := reader.WriteTo(io.Discard); done <- err }()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed source returned success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock WriteTo")
	}
}
