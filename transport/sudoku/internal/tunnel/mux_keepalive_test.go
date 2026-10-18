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

// captureConn is a net.Conn for muxSession tests: writes are recorded (or dropped) and reads block until Close.
type captureConn struct {
	net.Conn

	mu      sync.Mutex
	keep    bool
	written bytes.Buffer
	closed  chan struct{}
	once    sync.Once
}

func newCaptureConn(keep bool) *captureConn {
	return &captureConn{keep: keep, closed: make(chan struct{})}
}

func (c *captureConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *captureConn) Write(p []byte) (int, error) {
	if c.keep {
		c.mu.Lock()
		c.written.Write(p)
		c.mu.Unlock()
	}
	return len(p), nil
}

func (c *captureConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *captureConn) takeWritten() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]byte(nil), c.written.Bytes()...)
	c.written.Reset()
	return out
}

func TestMuxSession_IdleKeepaliveFiresOnlyAfterAQuietInterval(t *testing.T) {
	const interval = time.Minute
	conn := newCaptureConn(true)
	s := newMuxSession(conn, nil)
	t.Cleanup(func() { s.closeWithError(net.ErrClosed) })
	s.startKeepalive(time.Hour, muxKeepaliveIdle)

	tick := func() []byte {
		t.Helper()
		if !s.keepaliveTick(interval, muxKeepaliveIdle) {
			t.Fatal("tick reported a dead session")
		}
		return conn.takeWritten()
	}

	if err := s.sendFrame(muxFrameData, 1, []byte("x")); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}
	conn.takeWritten()

	if got := tick(); len(got) != 0 {
		t.Fatalf("keepalive sent although the session wrote during the interval: %x", got)
	}
	want := []byte{muxFrameData, 0, 0, 0, 0, 0, 0, 0, 0}
	if got := tick(); !bytes.Equal(got, want) {
		t.Fatalf("idle keepalive frame = %x, want %x", got, want)
	}
	// The keepalive frame is itself a write, so a quiet session pings every other tick.
	if got := tick(); len(got) != 0 {
		t.Fatalf("the keepalive frame did not count as a write: %x", got)
	}
	if got := tick(); !bytes.Equal(got, want) {
		t.Fatalf("second idle keepalive frame = %x, want %x", got, want)
	}
}

func TestMuxSession_PingKeepaliveClosesAfterMissingPongs(t *testing.T) {
	const interval = time.Minute
	s := newMuxSession(newCaptureConn(false), nil)
	t.Cleanup(func() { s.closeWithError(net.ErrClosed) })

	s.lastPong.Store(time.Now().Add(-4 * interval).UnixNano())
	if s.keepaliveTick(interval, muxKeepalivePing) {
		t.Fatal("tick kept a session that missed its pongs")
	}
	if !s.isClosed() {
		t.Fatal("session was not closed")
	}
}

func BenchmarkMuxSendFrame(b *testing.B) {
	payload := make([]byte, 64)
	for _, tc := range []struct {
		name string
		kind muxKeepaliveKind
	}{
		{"idle", muxKeepaliveIdle},
		{"ping", muxKeepalivePing},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s := newMuxSession(newCaptureConn(false), nil)
			defer s.closeWithError(net.ErrClosed)
			s.startKeepalive(time.Hour, tc.kind)

			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.sendFrame(muxFrameData, 1, payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
