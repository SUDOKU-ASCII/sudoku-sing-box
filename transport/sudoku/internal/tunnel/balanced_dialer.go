package tunnel

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync/atomic"
)

// StickyDialer allows callers to provide a stable affinity key for load balancing.
type StickyDialer interface {
	Dialer
	DialWithStickyKey(destAddrStr string, stickyKey string) (net.Conn, error)
}

// BalancedNode represents one outbound client node in the load-balancing pool.
type BalancedNode struct {
	ID     string
	Dialer Dialer
}

// BalancedDialer fans out requests to multiple outbound nodes.
//
// TCP connections use rendezvous hashing when a sticky key is available so the
// same site consistently lands on the same node while keeping distribution even
// across the pool. When the preferred node fails, it falls back to the next best
// candidate without random selection.
type BalancedDialer struct {
	nodes []BalancedNode
	rr    atomic.Uint64
}

func NewBalancedDialer(nodes []BalancedNode) (*BalancedDialer, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("load balancer requires at least one node")
	}

	cloned := make([]BalancedNode, 0, len(nodes))
	usedIDs := make(map[string]int, len(nodes))
	for i, node := range nodes {
		if node.Dialer == nil {
			return nil, fmt.Errorf("load balancer node %d has nil dialer", i)
		}
		id := strings.TrimSpace(node.ID)
		if id == "" {
			id = fmt.Sprintf("node-%d", i+1)
		}
		usedIDs[id]++
		if usedIDs[id] > 1 {
			id = fmt.Sprintf("%s#%d", id, usedIDs[id])
		}
		cloned = append(cloned, BalancedNode{
			ID:     id,
			Dialer: node.Dialer,
		})
	}

	return &BalancedDialer{nodes: cloned}, nil
}

func (d *BalancedDialer) Dial(destAddrStr string) (net.Conn, error) {
	return d.DialWithStickyKey(destAddrStr, StickyKeyForAddress(destAddrStr, nil))
}

func (d *BalancedDialer) DialWithStickyKey(destAddrStr string, stickyKey string) (net.Conn, error) {
	if len(d.nodes) == 0 {
		return nil, fmt.Errorf("load balancer has no nodes")
	}

	order := d.pickOrder(stickyKey)
	var errs []string
	for _, idx := range order {
		node := d.nodes[idx]
		conn, err := node.Dialer.Dial(destAddrStr)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", node.ID, err))
	}

	return nil, fmt.Errorf("all upstream nodes failed: %s", strings.Join(errs, "; "))
}

func (d *BalancedDialer) DialUDPOverTCP() (net.Conn, error) {
	if len(d.nodes) == 0 {
		return nil, fmt.Errorf("load balancer has no nodes")
	}

	order := d.pickRoundRobinOrder()
	var errs []string
	for _, idx := range order {
		node := d.nodes[idx]
		uotDialer, ok := node.Dialer.(UoTDialer)
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: udp over tcp unsupported", node.ID))
			continue
		}
		conn, err := uotDialer.DialUDPOverTCP()
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", node.ID, err))
	}

	return nil, fmt.Errorf("all upstream nodes failed for udp over tcp: %s", strings.Join(errs, "; "))
}

func (d *BalancedDialer) pickOrder(stickyKey string) []int {
	if strings.TrimSpace(stickyKey) == "" || len(d.nodes) == 1 {
		return d.pickRoundRobinOrder()
	}

	type candidate struct {
		idx   int
		score uint64
		id    string
	}

	candidates := make([]candidate, 0, len(d.nodes))
	for i, node := range d.nodes {
		candidates = append(candidates, candidate{
			idx:   i,
			score: rendezvousScore(stickyKey, node.ID),
			id:    node.ID,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].score > candidates[j].score
	})

	order := make([]int, 0, len(candidates))
	for _, cand := range candidates {
		order = append(order, cand.idx)
	}
	return order
}

func (d *BalancedDialer) pickRoundRobinOrder() []int {
	order := make([]int, 0, len(d.nodes))
	if len(d.nodes) == 0 {
		return order
	}
	start := int(d.rr.Add(1)-1) % len(d.nodes)
	for i := 0; i < len(d.nodes); i++ {
		order = append(order, (start+i)%len(d.nodes))
	}
	return order
}

func rendezvousScore(stickyKey string, nodeID string) uint64 {
	sum := sha256.Sum256([]byte(stickyKey + "\x00" + nodeID))
	return binary.BigEndian.Uint64(sum[:8])
}

func StickyKeyForAddress(destAddrStr string, destIP net.IP) string {
	if ip := normalizeStickyIP(destIP); ip != nil {
		return ip.String()
	}

	destAddrStr = strings.TrimSpace(destAddrStr)
	if destAddrStr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(destAddrStr)
	if err != nil {
		return strings.ToLower(destAddrStr)
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return strings.ToLower(destAddrStr)
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

func normalizeStickyIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip.To16()
}
