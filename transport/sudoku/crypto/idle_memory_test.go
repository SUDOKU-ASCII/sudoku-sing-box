package crypto

import (
	"net"
	"runtime"
	"testing"
)

// After bulk traffic in both directions, an idle record connection must hold
// no frame buffers: large frames are borrowed from framePool per call.
func TestIdleRecordConnHeap(t *testing.T) {
	const pairs = 100
	key := make([]byte, 32)
	payload := make([]byte, 256*1024)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	var conns []*RecordConn
	stop := make(chan struct{})
	for i := 0; i < pairs; i++ {
		a, b := net.Pipe()
		ra, err := NewRecordConn(a, "aes-128-gcm", key, key)
		if err != nil {
			t.Fatal(err)
		}
		rb, err := NewRecordConn(b, "aes-128-gcm", key, key)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, ra, rb)
		for _, pair := range [][2]*RecordConn{{ra, rb}, {rb, ra}} {
			go func(w *RecordConn) { _, _ = w.Write(payload) }(pair[0])
			// A small buffer forces the pending-plaintext path as well as direct reads.
			buf := make([]byte, 4096)
			for got := 0; got < len(payload); {
				n, err := pair[1].Read(buf)
				if err != nil {
					t.Fatal(err)
				}
				got += n
			}
		}
		for _, c := range []*RecordConn{ra, rb} {
			go func(c *RecordConn) {
				_, _ = c.Read(make([]byte, 1))
				<-stop
			}(c)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	close(stop)
	perConn := float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(len(conns))
	runtime.KeepAlive(conns)
	t.Logf("idle heap per record conn: %.0f B", perConn)
	// Before pooling each side kept up to two 64 KiB frames. What remains is the
	// pair of AEAD states.
	if perConn > 8192 {
		t.Fatalf("idle record conn holds %.0f B, want <= 8192", perConn)
	}
}
