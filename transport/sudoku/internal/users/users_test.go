package users

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
)

func hashOf(t *testing.T, privHex string) string {
	t.Helper()
	raw, err := hex.DecodeString(privHex)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func TestRegistryLookup(t *testing.T) {
	priv := hex.EncodeToString(make([]byte, 64))
	h := hashOf(t, priv)
	reg, err := NewRegistry([]config.UserConfig{
		{Alias: "a@x.com", Key: priv},
		{Key: "AABBCCDDEEFF0011"[:12]},
		{Alias: "banned", Key: "0123456789abcdef", Blocked: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if u := reg.Lookup(h); u == nil || u.Alias != "a@x.com" {
		t.Fatalf("private-key user not found: %+v", u)
	}
	if u := reg.Lookup("aabbccddeeff9999"); u == nil || u.Alias != "aabbccddeeff" {
		t.Fatalf("prefix user not found: %+v", u)
	}
	if u := reg.Lookup("0123456789abcdef"); u == nil || !u.Blocked {
		t.Fatalf("blocked user not found: %+v", u)
	}
	if reg.Lookup("ffffffffffffffff") != nil {
		t.Fatal("unknown hash must not match")
	}
	var nilReg *Registry
	if nilReg.Lookup(h) != nil {
		t.Fatal("nil registry must not match")
	}
}

func TestConfigUserValidation(t *testing.T) {
	bad := [][]config.UserConfig{
		{{Key: ""}},
		{{Key: "zzzzzzzzzzzzzzzz"}},
		{{Key: "abcd"}},
		{{Key: "0123456789abcdef", Bandwidth: []float64{1}}},
		{{Key: "0123456789abcdef", Bandwidth: []float64{1, -1}}},
		{{Key: "0123456789abcdef", Data: -1}},
		{{Key: "0123456789abcdef"}, {Key: "0123456789ABCDEF"}},
	}
	for i, us := range bad {
		c := &config.Config{Mode: "server", Users: us}
		if err := c.Finalize(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	c := &config.Config{Mode: "server", Users: []config.UserConfig{{Key: "0123456789abcdef"}}}
	if err := c.Finalize(); err != nil || c.Users[0].Alias != "0123456789abcdef" {
		t.Fatalf("defaults: err=%v alias=%q", err, c.Users[0].Alias)
	}
}

func TestThrottleWrite(t *testing.T) {
	// 0.8 Mbps = 100 KB/s toward the client.
	reg, _ := NewRegistry([]config.UserConfig{{Key: "0123456789abcdef", Bandwidth: []float64{0.8, 0}}})
	u := reg.Lookup("0123456789abcdef")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go io.Copy(io.Discard, b)

	c := u.WrapPayload(a)
	start := time.Now()
	if _, err := c.Write(make([]byte, 100_000)); err != nil {
		t.Fatal(err)
	}
	// Initial burst is ~5KB, so ~0.95s expected.
	if d := time.Since(start); d < 800*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("100KB at 100KB/s took %v", d)
	}
}

func TestThrottleUnlimitedIsPassthrough(t *testing.T) {
	reg, _ := NewRegistry([]config.UserConfig{{Key: "0123456789abcdef"}})
	a, _ := net.Pipe()
	if got := reg.Lookup("0123456789abcdef").WrapPayload(a); got != a {
		t.Fatal("unlimited user should not be wrapped")
	}
}

func TestDataQuotaKicksAndCountsBothDirections(t *testing.T) {
	// 1e-5 GB = 10,000 bytes.
	reg, _ := NewRegistry([]config.UserConfig{{Key: "0123456789abcdef", Data: 1e-5}})
	u := reg.Lookup("0123456789abcdef")
	a, b := net.Pipe()
	defer b.Close()
	w := u.WrapWire(a)
	go func() {
		buf := make([]byte, 4000)
		b.Write(buf) // rx 4000
		io.ReadFull(b, buf)
	}()
	buf := make([]byte, 4000)
	if _, err := io.ReadFull(w, buf); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(buf); err != nil { // tx 4000
		t.Fatal(err)
	}
	if got := u.used.Load(); got != 8000 || u.Exceeded() {
		t.Fatalf("used=%d exceeded=%v", got, u.Exceeded())
	}
	go io.Copy(io.Discard, b)
	if _, err := w.Write(make([]byte, 4000)); err != nil {
		t.Fatal(err)
	}
	if !u.Exceeded() {
		t.Fatal("quota should be exceeded")
	}
	if _, err := w.Write([]byte{1}); err == nil {
		t.Fatal("connection should have been closed by the quota")
	}
}

func TestPendingBytesChargedOnBind(t *testing.T) {
	reg, _ := NewRegistry([]config.UserConfig{{Key: "0123456789abcdef"}})
	u := reg.Lookup("0123456789abcdef")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	w := NewWireConn(a)
	go io.Copy(io.Discard, b)
	w.Write(make([]byte, 300))
	w.Bind(u)
	w.Write(make([]byte, 200))
	if got := u.used.Load(); got != 500 {
		t.Fatalf("used=%d", got)
	}
}
