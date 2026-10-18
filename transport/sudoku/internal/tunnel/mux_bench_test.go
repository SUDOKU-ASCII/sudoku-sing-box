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
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// muxLoopbackPair returns a client and a server muxSession joined by a loopback TCP connection;
// the server side opens a sink for every stream.
func muxLoopbackPair(tb testing.TB) (client, server *muxSession) {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	cc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		tb.Fatal(err)
	}
	sc := <-accepted
	server = newMuxSession(sc, func(st *muxStream, _ []byte) {
		_, _ = io.Copy(io.Discard, st)
	})
	client = newMuxSession(cc, nil)
	tb.Cleanup(func() {
		client.closeWithError(net.ErrClosed)
		server.closeWithError(net.ErrClosed)
	})
	return client, server
}

// BenchmarkMuxSmallWrites measures many streams writing small payloads at once (DNS-like and
// chat-like traffic), where per-frame write cost dominates.
func BenchmarkMuxSmallWrites(b *testing.B) {
	for _, tc := range []struct {
		name    string
		streams int
		size    int
	}{
		{"1stream-64B", 1, 64},
		{"16streams-64B", 16, 64},
		{"64streams-256B", 64, 256},
	} {
		b.Run(tc.name, func(b *testing.B) {
			client, _ := muxLoopbackPair(b)
			streams := make([]net.Conn, tc.streams)
			for i := range streams {
				st, err := openMuxStream(client, []byte{0})
				if err != nil {
					b.Fatal(err)
				}
				streams[i] = st
			}
			payload := make([]byte, tc.size)
			per := b.N/tc.streams + 1
			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			b.ResetTimer()
			var wg sync.WaitGroup
			for _, st := range streams {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < per; i++ {
						if _, err := st.Write(payload); err != nil {
							return
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}

// BenchmarkMuxBulk is one stream writing full-size frames; coalescing must not slow it down.
// The mux has no per-stream flow control (an overrun stream is reset), so the sender stays
// within 2 MiB of what the sink has consumed.
func BenchmarkMuxBulk(b *testing.B) {
	var consumed atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	cc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	server := newMuxSession(<-accepted, func(st *muxStream, _ []byte) {
		_, _ = io.Copy(countingWriter{&consumed}, st)
	})
	client := newMuxSession(cc, nil)
	defer client.closeWithError(net.ErrClosed)
	defer server.closeWithError(net.ErrClosed)

	st, err := openMuxStream(client, []byte{0})
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, muxMaxDataPayload)
	const window = 2 << 20
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for int64(i)*int64(len(payload))-consumed.Load() > window {
			runtime.Gosched()
		}
		if _, err := st.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}

type countingWriter struct{ n *atomic.Int64 }

func (w countingWriter) Write(p []byte) (int, error) {
	w.n.Add(int64(len(p)))
	return len(p), nil
}

// BenchmarkMuxRoundTrip is a request/response ping-pong on one stream: the latency a lone
// interactive connection sees through the sender.
func BenchmarkMuxRoundTrip(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Skipf("loopback unavailable: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	cc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	server := newMuxSession(<-accepted, func(st *muxStream, _ []byte) {
		_, _ = io.Copy(st, st)
	})
	client := newMuxSession(cc, nil)
	defer client.closeWithError(net.ErrClosed)
	defer server.closeWithError(net.ErrClosed)

	st, err := openMuxStream(client, []byte{0})
	if err != nil {
		b.Fatal(err)
	}
	msg := make([]byte, 64)
	reply := make([]byte, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.Write(msg); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(st, reply); err != nil {
			b.Fatal(err)
		}
	}
}
