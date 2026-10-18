package tunnel

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
)

func TestMuxChunksSurvivePartialReadsAndRemoteClose(t *testing.T) {
	stream := newMuxStream(nil, 1)
	defer stream.Close()
	var want []byte
	for _, n := range []int{1, 1025, 32768, muxMaxFrameSize} {
		chunk := acquireMuxChunk(n)
		for i := range chunk.data {
			chunk.data[i] = byte(i + n)
		}
		want = append(want, chunk.data...)
		if err := stream.enqueue(chunk); err != nil {
			t.Fatal(err)
		}
	}
	stream.closeRemoteWrite()
	var got bytes.Buffer
	buf := make([]byte, 37)
	for {
		n, err := stream.Read(buf)
		got.Write(buf[:n])
		// Reuse other blocks while an earlier block is only partly consumed.
		chunk := acquireMuxChunk(32768)
		clear(chunk.data)
		chunk.release()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("queued data was overwritten or reordered")
	}
	if stream.head != nil || stream.tail != nil || stream.queuedBytes != 0 {
		t.Fatal("drained queue retains blocks")
	}
}

func TestMuxChunksConcurrentClose(t *testing.T) {
	for range 50 {
		stream := newMuxStream(nil, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				chunk := acquireMuxChunk(1024)
				for i := range chunk.data {
					chunk.data[i] = byte(i)
				}
				_ = stream.enqueue(chunk)
			}
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, 17)
			for range 10 {
				_, _ = stream.Read(buf)
			}
			stream.abort(net.ErrClosed)
			_ = stream.Close()
		}()
		wg.Wait()
		if stream.head != nil || stream.tail != nil || stream.queuedBytes != 0 {
			t.Fatal("closed queue retains blocks")
		}
	}
}
