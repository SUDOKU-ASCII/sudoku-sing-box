package connutil

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
)

type buffersWriterFunc func([]byte) (int, error)

func (f buffersWriterFunc) Write(p []byte) (int, error) { return f(p) }

type shortBuffersWriter struct{ calls int }

func (*shortBuffersWriter) Write([]byte) (int, error) { panic("unexpected fallback") }

func (w *shortBuffersWriter) WriteBuffers(net.Buffers) (int64, error) {
	w.calls++
	return 1, nil
}

func TestWriteBuffersRejectsShortBatch(t *testing.T) {
	w := new(shortBuffersWriter)
	n, err := WriteBuffers(w, net.Buffers{[]byte("head"), []byte("body")})
	if n != 1 || err != io.ErrShortWrite || w.calls != 1 {
		t.Fatalf("batch write = %d, %v (%d calls)", n, err, w.calls)
	}
}

func TestWriteBuffersFallback(t *testing.T) {
	buffers := net.Buffers{nil, []byte("header"), {}, []byte("payload")}
	var out bytes.Buffer
	n, err := WriteBuffers(buffersWriterFunc(func(p []byte) (int, error) {
		return out.Write(p[:min(2, len(p))])
	}), buffers)
	if n != 13 || err != nil || out.String() != "headerpayload" {
		t.Fatalf("write = %d, %v, %q", n, err, out.String())
	}
	if len(buffers) != 4 || string(buffers[1]) != "header" || string(buffers[3]) != "payload" {
		t.Fatal("caller buffers were modified")
	}
	writeErr := errors.New("write failed")
	for _, tc := range []struct {
		n    int
		err  error
		want int64
	}{
		{0, nil, 0}, {-1, nil, 0}, {7, nil, 0}, {2, writeErr, 2},
	} {
		n, err := WriteBuffers(buffersWriterFunc(func([]byte) (int, error) {
			return tc.n, tc.err
		}), buffers)
		wantErr := tc.err
		if wantErr == nil {
			wantErr = io.ErrShortWrite
		}
		if n != tc.want || err != wantErr {
			t.Fatalf("write = %d, %v; want %d, %v", n, err, tc.want, wantErr)
		}
	}
}
