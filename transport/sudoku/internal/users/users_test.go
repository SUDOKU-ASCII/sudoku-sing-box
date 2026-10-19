package users

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestStatsSplitDirectionsInConfigOrder(t *testing.T) {
	reg, err := NewRegistry([]config.UserConfig{
		{Key: "0123456789abcdef", Alias: "full", Data: 1},
		{Key: "aabbccddeeff", Alias: "prefix", Blocked: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	u := reg.Lookup("0123456789abcdef")
	a, b := net.Pipe()
	defer b.Close()
	w := u.WrapWire(a)
	go func() {
		b.Write(make([]byte, 30)) // up 30
		io.Copy(io.Discard, b)
	}()
	if _, err := io.ReadFull(w, make([]byte, 30)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(make([]byte, 70)); err != nil { // down 70
		t.Fatal(err)
	}

	stats := reg.Stats()
	want := []Stat{
		{Name: "full", Conns: 1, Up: 30, Down: 70, Used: 100, Limit: 1e9},
		{Name: "prefix", Blocked: true},
	}
	if !slices.Equal(stats, want) {
		t.Fatalf("Stats() = %+v, want %+v", stats, want)
	}
	w.Close()
	if got := reg.Stats()[0].Conns; got != 0 {
		t.Fatalf("Conns after close = %d", got)
	}
	var nilReg *Registry
	if nilReg.Stats() != nil {
		t.Fatal("nil registry must have no stats")
	}
}

func TestUsagePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.usage.json")
	cfgs := []config.UserConfig{
		{Key: "0123456789abcdef", Data: 1e-5},
		{Key: "aabbccddeeff"},
	}
	reg, _ := NewRegistry(cfgs)
	stop, err := reg.TrackUsage(context.Background(), path)
	if err != nil {
		t.Fatalf("missing file must not fail: %v", err)
	}
	reg.Lookup("0123456789abcdef").addTraffic(upload, 10_000)
	reg.Lookup("aabbccddeeff0000").addTraffic(upload, 42)
	stop() // a clean shutdown saves even though the ticker never fired

	// The restarted server restores this month and drops users removed from the config.
	restarted, _ := NewRegistry(cfgs[:1])
	stop, err = restarted.TrackUsage(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if u := restarted.Lookup("0123456789abcdef"); u.used.Load() != 10_000 || !u.Exceeded() {
		t.Fatalf("restored used=%d exceeded=%v", u.used.Load(), u.Exceeded())
	}
	stop()
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "aabbccddeeff") {
		t.Fatalf("removed user still saved: %s", b)
	}

	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.TrackUsage(context.Background(), path); err == nil {
		t.Fatal("corrupt file must fail instead of silently resetting quotas")
	}
}

func TestUsageResetsMonthly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.usage.json")
	old := `{"month":"2000-01","used":{"0123456789abcdef":10000}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, _ := NewRegistry([]config.UserConfig{{Key: "0123456789abcdef", Data: 1e-5}})
	stop, err := reg.TrackUsage(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	u := reg.Lookup("0123456789abcdef")
	if u.used.Load() != 0 {
		t.Fatalf("usage from an earlier month restored: %d", u.used.Load())
	}

	// Rolling over while running clears quotas, including the kicked state.
	u.addTraffic(upload, 10_000)
	reg.resetUsage()
	if u.Exceeded() || u.kicked.Load() {
		t.Fatalf("reset left used=%d kicked=%v", u.used.Load(), u.kicked.Load())
	}
}
