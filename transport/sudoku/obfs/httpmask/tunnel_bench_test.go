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
package httpmask

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

const benchChunkBytes = 16 * 1024

var benchModes = []string{"stream", "poll"}

// startBenchServer serves srv on loopback and queues every accepted tunnel conn on the returned channel.
func startBenchServer(tb testing.TB, srv *TunnelServer) (addr string, tunnels <-chan net.Conn) {
	tb.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}
	queue := make(chan net.Conn, 1024)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				res, c, err := srv.HandleConn(conn)
				switch {
				case err != nil:
					_ = conn.Close()
				case res == HandleStartTunnel:
					queue <- c
				case res == HandlePassThrough:
					_ = c.Close()
				}
			}(raw)
		}
	}()
	tb.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), queue
}

func newBenchServer(mode string) *TunnelServer {
	return NewTunnelServer(TunnelServerOptions{Mode: mode, SessionTTL: time.Minute})
}

func dialBenchSession(tb testing.TB, addr string, tunnels <-chan net.Conn, mode string) (client, server net.Conn) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialTunnel(ctx, addr, TunnelDialOptions{Mode: mode})
	if err != nil {
		tb.Fatalf("dial %s: %v", mode, err)
	}
	select {
	case server = <-tunnels:
	case <-time.After(10 * time.Second):
		tb.Fatalf("no server tunnel conn for %s", mode)
	}
	return client, server
}

func BenchmarkSplitUpload(b *testing.B) {
	for _, mode := range benchModes {
		b.Run(mode, func(b *testing.B) {
			addr, tunnels := startBenchServer(b, newBenchServer(mode))
			client, server := dialBenchSession(b, addr, tunnels, mode)
			defer client.Close()
			defer server.Close()

			drained := make(chan struct{})
			go func() {
				_, _ = io.Copy(io.Discard, server)
				close(drained)
			}()

			chunk := make([]byte, benchChunkBytes)
			b.SetBytes(benchChunkBytes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := client.Write(chunk); err != nil {
					b.Fatalf("write: %v", err)
				}
			}
			_ = client.(interface{ CloseWrite() error }).CloseWrite()
			<-drained
		})
	}
}

func BenchmarkSplitDownload(b *testing.B) {
	for _, mode := range benchModes {
		b.Run(mode, func(b *testing.B) {
			addr, tunnels := startBenchServer(b, newBenchServer(mode))
			client, server := dialBenchSession(b, addr, tunnels, mode)
			defer client.Close()
			defer server.Close()

			chunk := make([]byte, benchChunkBytes)
			b.SetBytes(benchChunkBytes)
			b.ReportAllocs()
			b.ResetTimer()
			go func() {
				for i := 0; i < b.N; i++ {
					if _, err := server.Write(chunk); err != nil {
						return
					}
				}
				_ = server.(interface{ CloseWrite() error }).CloseWrite()
			}()
			if n, err := io.Copy(io.Discard, client); err != nil || n != int64(b.N)*benchChunkBytes {
				b.Fatalf("download: n=%d err=%v", n, err)
			}
		})
	}
}

// BenchmarkSplitPingPong measures the per-request cost of small interactive exchanges: each op is one upload
// request carrying a short message plus one downlink chunk carrying the reply.
func BenchmarkSplitPingPong(b *testing.B) {
	const messageBytes = 64
	for _, mode := range benchModes {
		b.Run(mode, func(b *testing.B) {
			addr, tunnels := startBenchServer(b, newBenchServer(mode))
			client, server := dialBenchSession(b, addr, tunnels, mode)
			defer client.Close()
			defer server.Close()

			go func() {
				msg := make([]byte, messageBytes)
				for {
					if _, err := io.ReadFull(server, msg); err != nil {
						return
					}
					if _, err := server.Write(msg); err != nil {
						return
					}
				}
			}()

			msg := make([]byte, messageBytes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := client.Write(msg); err != nil {
					b.Fatalf("write: %v", err)
				}
				if _, err := io.ReadFull(client, msg); err != nil {
					b.Fatalf("read: %v", err)
				}
			}
		})
	}
}
