/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package tunnel

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// Writers of many streams share the batch queue; every stream must still receive its bytes
// complete and in order, whether its frames are small (queued) or large (written directly).
func TestMuxSession_CoalescedWritesKeepStreamsIntact(t *testing.T) {
	const streams = 24
	sizes := []int{1, 7, 64, muxCoalesceMaxPayload, muxCoalesceMaxPayload + 1, 40 << 10}

	var mu sync.Mutex
	got := make(map[uint32][]byte)
	done := make(chan struct{}, streams)
	clientConn, serverConn := net.Pipe()
	server := newMuxSession(serverConn, func(st *muxStream, _ []byte) {
		data, _ := io.ReadAll(st)
		mu.Lock()
		got[st.id] = data
		mu.Unlock()
		done <- struct{}{}
	})
	client := newMuxSession(clientConn, nil)
	t.Cleanup(func() {
		client.closeWithError(net.ErrClosed)
		server.closeWithError(net.ErrClosed)
	})

	want := make(map[uint32][]byte)
	var wmu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		st, err := openMuxStream(client, []byte{0})
		if err != nil {
			t.Fatal(err)
		}
		ms := st.(*muxStream)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sent bytes.Buffer
			for round := 0; round < 40; round++ {
				size := sizes[(round+int(ms.id))%len(sizes)]
				p := make([]byte, size)
				for j := range p {
					p[j] = byte(round) ^ byte(ms.id) ^ byte(j)
				}
				sent.Write(p)
				if _, err := ms.Write(p); err != nil {
					t.Errorf("stream %d write: %v", ms.id, err)
					return
				}
			}
			if err := ms.CloseWrite(); err != nil {
				t.Errorf("stream %d close: %v", ms.id, err)
			}
			wmu.Lock()
			want[ms.id] = sent.Bytes()
			wmu.Unlock()
		}()
	}
	wg.Wait()

	for i := 0; i < streams; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d streams completed: a queued frame was stranded", i, streams)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for id, w := range want {
		if !bytes.Equal(got[id], w) {
			t.Fatalf("stream %d received %d bytes, want %d (or content differs)", id, len(got[id]), len(w))
		}
	}
}

// A frame queued behind a failing write must not leave the session half open.
func TestMuxSession_QueuedWriteFailureClosesSession(t *testing.T) {
	a, b := net.Pipe()
	s := newMuxSession(a, nil)
	_ = b.Close()
	if err := s.sendFrame(muxFrameData, 1, []byte("x")); err == nil {
		t.Fatal("expected a write error")
	}
	if !s.isClosed() {
		t.Fatal("session stayed open after a failed write")
	}
}

// slowWriteConn makes every write take long enough for other writers to queue frames meanwhile.
type slowWriteConn struct{ net.Conn }

func (c slowWriteConn) Write(p []byte) (int, error) {
	time.Sleep(3 * time.Millisecond)
	return c.Conn.Write(p)
}

// Frames queued while another goroutine is writing are flushed by that goroutine; without its
// recheck the last frames of a burst would wait for the next write that may never come.
func TestMuxSession_FramesQueuedDuringAWriteAreNotStranded(t *testing.T) {
	const writers = 12
	clientConn, serverConn := net.Pipe()
	received := make(chan struct{}, writers)
	server := newMuxSession(serverConn, func(st *muxStream, _ []byte) {
		buf := make([]byte, 8)
		if _, err := io.ReadFull(st, buf); err == nil {
			received <- struct{}{}
		}
	})
	client := newMuxSession(slowWriteConn{clientConn}, nil)
	t.Cleanup(func() {
		client.closeWithError(net.ErrClosed)
		server.closeWithError(net.ErrClosed)
	})

	streams := make([]*muxStream, writers)
	for i := range streams {
		st, err := openMuxStream(client, []byte{0})
		if err != nil {
			t.Fatal(err)
		}
		streams[i] = st.(*muxStream)
	}
	var wg sync.WaitGroup
	for _, st := range streams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = st.Write([]byte("12345678"))
		}()
	}
	wg.Wait()

	for i := 0; i < writers; i++ {
		select {
		case <-received:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of %d single-frame writes arrived", i, writers)
		}
	}
}
