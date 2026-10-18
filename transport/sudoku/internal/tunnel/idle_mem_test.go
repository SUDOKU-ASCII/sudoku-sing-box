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
	"net"
	"runtime"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// idleTunnelFixture runs a server whose handler parks on the first KIP message, as an idle
// proxied connection does, and returns a dialer for it.
func idleTunnelFixture(tb testing.TB, onIdle func(net.Conn)) *StandardDialer {
	tb.Helper()
	cfg := &config.Config{
		Mode:               "server",
		Transport:          "tcp",
		Key:                "idle-mem-key",
		AEAD:               "chacha20-poly1305",
		PaddingMin:         10,
		PaddingMax:         20,
		ASCII:              "prefer_entropy",
		EnablePureDownlink: true,
	}
	table := sudoku.NewTable(cfg.Key, cfg.ASCII)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Skipf("loopback unavailable: %v", err)
	}
	tb.Cleanup(func() { _ = ln.Close() })
	cfg.ServerAddress = ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sConn, _, err := ServerHandshake(c, cfg, []*sudoku.Table{table})
				if err != nil {
					_ = c.Close()
					return
				}
				onIdle(sConn)
			}()
		}
	}()
	clientCfg := *cfg
	clientCfg.Mode = "client"
	return &StandardDialer{BaseDialer: BaseDialer{Config: &clientCfg, Tables: []*sudoku.Table{table}}}
}

// BenchmarkIdleConnMemory reports the live heap one idle tunnel connection costs, counting both
// its client and its server side (they share this process).
func BenchmarkIdleConnMemory(b *testing.B) {
	const conns = 200
	for i := 0; i < b.N; i++ {
		var held sync.WaitGroup
		release := make(chan struct{})
		serverSide := make(chan struct{}, conns)
		d := idleTunnelFixture(b, func(c net.Conn) {
			defer c.Close()
			serverSide <- struct{}{}
			held.Add(1)
			defer held.Done()
			_, _ = ReadKIPMessage(c)
			<-release
		})

		before := heapInUse()
		clients := make([]net.Conn, 0, conns)
		for j := 0; j < conns; j++ {
			c, err := d.DialBase()
			if err != nil {
				b.Fatal(err)
			}
			clients = append(clients, c)
		}
		for j := 0; j < conns; j++ {
			<-serverSide
		}
		after := heapInUse()
		b.ReportMetric(float64(after-before)/conns, "heap-B/conn")

		for _, c := range clients {
			_ = c.Close()
		}
		close(release)
		held.Wait()
	}
}

// BenchmarkIdleStreamMemory reports the heap retained by one idle mux stream. A busy proxy
// holds tens of thousands of them, so every word in muxStream is multiplied.
func BenchmarkIdleStreamMemory(b *testing.B) {
	const streams = 20000
	sess := &muxSession{streams: make(map[uint32]*muxStream)}
	for i := 0; i < b.N; i++ {
		held := make([]*muxStream, streams)
		before := heapInUse()
		for j := range held {
			held[j] = newMuxStream(sess, uint32(j+1))
		}
		after := heapInUse()
		b.ReportMetric(float64(after-before)/streams, "heap-B/stream")
		runtime.KeepAlive(held)
	}
}
