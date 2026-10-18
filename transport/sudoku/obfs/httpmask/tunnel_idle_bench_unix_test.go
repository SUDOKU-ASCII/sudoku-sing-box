//go:build unix

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
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"
)

const (
	idleBenchSessions = 200
	idleBenchWindow   = 2 * time.Second
)

func processCPUTime(tb testing.TB) time.Duration {
	tb.Helper()
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		tb.Fatalf("getrusage: %v", err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// liveMemory returns the live heap and the goroutine stack bytes in use after a collection.
func liveMemory() (heap, stack int64) {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc), int64(ms.StackInuse)
}

// BenchmarkSplitIdleSessions reports the CPU, heap and goroutine stacks that idle established sessions cost on both ends.
func BenchmarkSplitIdleSessions(b *testing.B) {
	for _, mode := range benchModes {
		b.Run(mode, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				heapBefore, stackBefore := liveMemory()
				addr, tunnels := startBenchServer(b, newBenchServer(mode))
				var conns []net.Conn
				for j := 0; j < idleBenchSessions; j++ {
					client, server := dialBenchSession(b, addr, tunnels, mode)
					conns = append(conns, client, server)
				}
				time.Sleep(200 * time.Millisecond) // let dial-time work settle
				heapAfter, stackAfter := liveMemory()
				b.ReportMetric(float64(heapAfter-heapBefore)/idleBenchSessions, "heap-B/session")
				b.ReportMetric(float64(stackAfter-stackBefore)/idleBenchSessions, "stack-B/session")

				cpuBefore := processCPUTime(b)
				b.StartTimer()
				time.Sleep(idleBenchWindow)
				b.StopTimer()
				cpu := processCPUTime(b) - cpuBefore
				b.ReportMetric(float64(cpu.Milliseconds()), "cpu-ms/op")
				for _, c := range conns {
					_ = c.Close()
				}
			}
		})
	}
}
