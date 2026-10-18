package connutil

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// opaque hides WriterTo and ReaderFrom so Copy takes the relay-buffer path.
type opaqueReader struct{ io.Reader }
type opaqueWriter struct{ io.Writer }

func TestCopyAdaptiveBuffer(t *testing.T) {
	data := make([]byte, 300*1024+17)
	for i := range data {
		data[i] = byte(i * 7)
	}
	readers := map[string]func() io.Reader{
		"whole":    func() io.Reader { return bytes.NewReader(data) },
		"one-byte": func() io.Reader { return iotest.OneByteReader(bytes.NewReader(data[:4096])) },
		"half":     func() io.Reader { return iotest.HalfReader(bytes.NewReader(data)) },
	}
	for name, mk := range readers {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			n, err := Copy(opaqueWriter{&out}, opaqueReader{mk()})
			if err != nil || n != int64(out.Len()) {
				t.Fatalf("Copy = (%d, %v), wrote %d", n, err, out.Len())
			}
			want, _ := io.ReadAll(mk())
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatal("relayed bytes differ")
			}
		})
	}
}

type failingWriter struct{ after int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.after <= 0 {
		return 0, errors.New("write failed")
	}
	n := min(w.after, len(p))
	w.after -= n
	if n < len(p) {
		return n, errors.New("write failed")
	}
	return n, nil
}

func TestCopyReportsWriteErrors(t *testing.T) {
	n, err := Copy(&failingWriter{after: 5000}, opaqueReader{bytes.NewReader(make([]byte, 100000))})
	if err == nil || n != 5000 {
		t.Fatalf("Copy = (%d, %v), want 5000 and an error", n, err)
	}
}
