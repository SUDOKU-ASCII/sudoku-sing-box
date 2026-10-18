package tunnel

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type muxWriterFunc func([]byte) (int, error)

func (f muxWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestMuxWriteToPreservesUnwrittenData(t *testing.T) {
	writeErr := errors.New("destination failed")
	for _, tc := range []struct {
		name string
		n    int
		err  error
	}{
		{"short", 3, nil},
		{"zero", 0, nil},
		{"partial_error", 3, writeErr},
		{"full_error", 29, writeErr},
		{"negative_count", -1, nil},
		{"excess_count", 30, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := newMuxStream(nil, 1)
			defer stream.Close()
			payload := bytes.Repeat([]byte("abcdefgh"), 4)
			for range 2 {
				chunk := acquireMuxChunk(len(payload))
				copy(chunk.data, payload)
				if err := stream.enqueue(chunk); err != nil {
					t.Fatal(err)
				}
			}
			stream.closeRemoteWrite()
			prefix := make([]byte, 3)
			if _, err := io.ReadFull(stream, prefix); err != nil {
				t.Fatal(err)
			}
			var written []byte
			n, err := io.Copy(muxWriterFunc(func(p []byte) (int, error) {
				if tc.n >= 0 && tc.n <= len(p) {
					written = append(written, p[:tc.n]...)
				}
				return tc.n, tc.err
			}), stream)
			wantErr := tc.err
			if wantErr == nil {
				wantErr = io.ErrShortWrite
			}
			if n != int64(len(written)) || err != wantErr {
				t.Fatalf("copy = %d, %v; want %d, %v", n, err, len(written), wantErr)
			}
			rest, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			got := append(append(prefix, written...), rest...)
			if !bytes.Equal(got, bytes.Repeat(payload, 2)) {
				t.Fatal("short write lost or reordered the queued suffix")
			}
			if stream.queuedBytes != 0 {
				t.Fatal("drained stream retains unread bytes")
			}
		})
	}
}

func TestMuxWriteToOwnsBlockDuringConcurrentClose(t *testing.T) {
	for _, closeRead := range []bool{false, true} {
		stream := newMuxStream(nil, 1)
		pool := new(sync.Pool)
		chunk := &muxChunk{data: bytes.Repeat([]byte{0x5a}, muxMaxFrameSize), pool: pool}
		if err := stream.enqueue(chunk); err != nil {
			t.Fatal(err)
		}
		entered, unblock := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		var once sync.Once
		releaseWriter := func() { once.Do(func() { close(unblock) }) }
		defer releaseWriter()
		go func() {
			_, err := io.Copy(muxWriterFunc(func(p []byte) (int, error) {
				if &p[0] != &chunk.data[0] {
					t.Error("relay copied the received block")
				}
				close(entered)
				<-unblock
				return len(p), nil
			}), stream)
			done <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("destination was not reached")
		}
		// The detached block still counts toward the unread-data limit.
		for range muxMaxQueuedBytesPerStream/muxMaxFrameSize - 1 {
			if err := stream.enqueue(acquireMuxChunk(muxMaxFrameSize)); err != nil {
				t.Fatal(err)
			}
		}
		if err := stream.enqueue(acquireMuxChunk(1)); err != errMuxReceiveQueueFull {
			t.Fatalf("in-flight block bypassed queue limit: %v", err)
		}
		if closeRead {
			// A session error must not prevent a later local CloseRead from
			// releasing unread blocks.
			stream.abort(net.ErrClosed)
			_ = stream.CloseRead()
		} else {
			_ = stream.Close()
		}
		if got := pool.Get(); got != nil {
			t.Fatal("block recycled while the destination was still using it")
		}
		if stream.queuedBytes != muxMaxFrameSize || stream.head != nil {
			t.Fatal("close did not release exactly the queued blocks")
		}
		releaseWriter()
		select {
		case err := <-done:
			if err != io.ErrClosedPipe && err != net.ErrClosed {
				t.Fatalf("copy after local close: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("copy did not finish after local close")
		}
		if stream.queuedBytes != 0 {
			t.Fatal("completed write retained its block")
		}
	}
}

func TestMuxWriteToDrainsBeforeSessionError(t *testing.T) {
	stream := newMuxStream(nil, 1)
	defer stream.Close()
	chunk := acquireMuxChunk(1024)
	copy(chunk.data, bytes.Repeat([]byte{0x31}, 1024))
	_ = stream.enqueue(chunk)
	stream.abort(net.ErrClosed)
	var out bytes.Buffer
	n, err := io.Copy(&out, stream)
	if n != 1024 || err != net.ErrClosed || !bytes.Equal(out.Bytes(), bytes.Repeat([]byte{0x31}, 1024)) {
		t.Fatalf("copy = %d, %v, payload length %d", n, err, out.Len())
	}
}
