package sudoku

import (
	"net"
	"runtime"
	"testing"
)

// idleHeapPerConn builds pairs of connected codecs, runs bulk traffic through
// each in both directions, leaves every side blocked in Read, and returns the
// live heap per codec conn. Pipes are unbuffered, so what remains is the
// codecs' own buffers.
func idleHeapPerConn(tb testing.TB, pairs int, wrap func(net.Conn) net.Conn) float64 {
	tb.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	conns := make([]net.Conn, 0, 2*pairs)
	stop := make(chan struct{})
	payload := make([]byte, 256*1024)
	for i := 0; i < pairs; i++ {
		a, b := net.Pipe()
		ca, cb := wrap(a), wrap(b)
		conns = append(conns, ca, cb)
		for _, pair := range [][2]net.Conn{{ca, cb}, {cb, ca}} {
			go func(w net.Conn) { _, _ = w.Write(payload) }(pair[0])
			buf := make([]byte, 4096)
			for got := 0; got < len(payload); {
				n, err := pair[1].Read(buf)
				if err != nil {
					tb.Fatal(err)
				}
				got += n
			}
		}
		for _, c := range [2]net.Conn{ca, cb} {
			go func(c net.Conn) {
				one := make([]byte, 1)
				_, _ = c.Read(one)
				<-stop
			}(c)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	close(stop)
	for _, c := range conns {
		_ = c.Close()
	}
	runtime.KeepAlive(conns)
	return float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(len(conns))
}

// An idle codec connection must not keep wire or encode buffers: those are
// borrowed from a pool only while data is moving.
func TestIdleConnHeap(t *testing.T) {
	table := mustTable(t, "idle-memory", "prefer_ascii")
	cases := []struct {
		name string
		wrap func(net.Conn) net.Conn
	}{
		{"pure", func(c net.Conn) net.Conn { return NewConn(c, table, 5, 15, false) }},
		{"packed", func(c net.Conn) net.Conn { return NewPackedConn(c, table, 5, 15) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perConn := idleHeapPerConn(t, 100, tc.wrap)
			t.Logf("idle heap per conn: %.0f B", perConn)
			// Before pooling this was above 64 KiB: a 32 KiB bufio reader plus a
			// retained encode buffer of up to 32 KiB.
			if perConn > 4096 {
				t.Fatalf("idle conn holds %.0f B, want <= 4096", perConn)
			}
		})
	}
}
