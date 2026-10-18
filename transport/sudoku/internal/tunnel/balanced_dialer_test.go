package tunnel

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
)

type mockBalancedDialer struct {
	dialErr  error
	dials    int
	udpDials int
}

func (m *mockBalancedDialer) Dial(destAddrStr string) (net.Conn, error) {
	m.dials++
	if m.dialErr != nil {
		return nil, m.dialErr
	}
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, nil
}

func (m *mockBalancedDialer) DialUDPOverTCP() (net.Conn, error) {
	m.udpDials++
	if m.dialErr != nil {
		return nil, m.dialErr
	}
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, nil
}

func TestBalancedDialerStickySelectionIsStable(t *testing.T) {
	left := &mockBalancedDialer{}
	right := &mockBalancedDialer{}

	lb, err := NewBalancedDialer([]BalancedNode{
		{ID: "left", Dialer: left},
		{ID: "right", Dialer: right},
	})
	if err != nil {
		t.Fatalf("NewBalancedDialer error: %v", err)
	}

	preferredID := rankedNodeIDs("example.com", "left", "right")[0]

	for i := 0; i < 8; i++ {
		conn, err := lb.DialWithStickyKey("example.com:443", "example.com")
		if err != nil {
			t.Fatalf("DialWithStickyKey error: %v", err)
		}
		_ = conn.Close()
	}

	if preferredID == "left" && left.dials != 8 {
		t.Fatalf("expected sticky node left to handle all dials, got %d", left.dials)
	}
	if preferredID == "right" && right.dials != 8 {
		t.Fatalf("expected sticky node right to handle all dials, got %d", right.dials)
	}
	if left.dials+right.dials != 8 {
		t.Fatalf("unexpected total dials: left=%d right=%d", left.dials, right.dials)
	}
}

func TestBalancedDialerFallsBackToNextCandidate(t *testing.T) {
	primary := &mockBalancedDialer{dialErr: fmt.Errorf("boom")}
	secondary := &mockBalancedDialer{}

	lb, err := NewBalancedDialer([]BalancedNode{
		{ID: "primary", Dialer: primary},
		{ID: "secondary", Dialer: secondary},
	})
	if err != nil {
		t.Fatalf("NewBalancedDialer error: %v", err)
	}

	key := findStickyKeyForFirstNode("primary", "secondary")
	conn, err := lb.DialWithStickyKey("example.com:443", key)
	if err != nil {
		t.Fatalf("DialWithStickyKey error: %v", err)
	}
	_ = conn.Close()

	if primary.dials != 1 {
		t.Fatalf("expected one attempt on primary, got %d", primary.dials)
	}
	if secondary.dials != 1 {
		t.Fatalf("expected fallback dial on secondary, got %d", secondary.dials)
	}
}

func TestBalancedDialerUDPUsesRoundRobin(t *testing.T) {
	left := &mockBalancedDialer{}
	right := &mockBalancedDialer{}

	lb, err := NewBalancedDialer([]BalancedNode{
		{ID: "left", Dialer: left},
		{ID: "right", Dialer: right},
	})
	if err != nil {
		t.Fatalf("NewBalancedDialer error: %v", err)
	}

	for i := 0; i < 4; i++ {
		conn, err := lb.DialUDPOverTCP()
		if err != nil {
			t.Fatalf("DialUDPOverTCP error: %v", err)
		}
		_ = conn.Close()
	}

	if left.udpDials != 2 || right.udpDials != 2 {
		t.Fatalf("unexpected udp dial distribution: left=%d right=%d", left.udpDials, right.udpDials)
	}
}

func TestBalancedDialerMakesDuplicateNodeIDsUnique(t *testing.T) {
	first := &mockBalancedDialer{}
	second := &mockBalancedDialer{}

	lb, err := NewBalancedDialer([]BalancedNode{
		{ID: "dup", Dialer: first},
		{ID: "dup", Dialer: second},
	})
	if err != nil {
		t.Fatalf("NewBalancedDialer error: %v", err)
	}

	conn, err := lb.DialWithStickyKey("example.com:443", "example.com")
	if err != nil {
		t.Fatalf("DialWithStickyKey error: %v", err)
	}
	_ = conn.Close()

	if first.dials+second.dials != 1 {
		t.Fatalf("unexpected total dials: first=%d second=%d", first.dials, second.dials)
	}
}

func TestStickyKeyForAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
		ip   net.IP
		want string
	}{
		{name: "host", addr: "Example.com:443", want: "example.com"},
		{name: "ipv6", addr: "[2001:db8::1]:443", want: "2001:db8::1"},
		{name: "ipv4", addr: "1.2.3.4:80", want: "1.2.3.4"},
		{name: "explicit ip", addr: "example.com:443", ip: net.ParseIP("1.2.3.4"), want: "1.2.3.4"},
	}

	for _, tc := range tests {
		if got := StickyKeyForAddress(tc.addr, tc.ip); got != tc.want {
			t.Fatalf("%s: StickyKeyForAddress(%q) = %q, want %q", tc.name, tc.addr, got, tc.want)
		}
	}
}

func findStickyKeyForFirstNode(firstID string, otherID string) string {
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		if rankedNodeIDs(key, firstID, otherID)[0] == firstID {
			return key
		}
	}
	panic("no sticky key found")
}

func rankedNodeIDs(stickyKey string, ids ...string) []string {
	type candidate struct {
		id    string
		score uint64
	}

	candidates := make([]candidate, 0, len(ids))
	for _, id := range ids {
		sum := sha256.Sum256([]byte(stickyKey + "\x00" + id))
		candidates = append(candidates, candidate{
			id:    id,
			score: binary.BigEndian.Uint64(sum[:8]),
		})
	}

	for i := 0; i < len(candidates); i++ {
		best := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[best].score || (candidates[j].score == candidates[best].score && candidates[j].id < candidates[best].id) {
				best = j
			}
		}
		candidates[i], candidates[best] = candidates[best], candidates[i]
	}

	ranked := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, candidate.id)
	}
	return ranked
}
