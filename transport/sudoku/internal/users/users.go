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

// Package users implements the server-side user whitelist: identification by handshake
// user hash, payload bandwidth limiting and wire traffic quota.
package users

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/logx"
)

const bytesPerGB = 1e9

// Registry resolves handshake user hashes to users. A nil Registry means "no whitelist".
type Registry struct {
	exact    map[string]*User // full 16-char hashes
	prefixed []*User          // shorter prefixes, in config order
	all      []*User          // every user, in config order
}

// Directions index per-direction byte counters: bytes read from the client are uploads.
const (
	upload = iota
	download
)

type User struct {
	Alias   string
	Blocked bool

	prefix string
	// toClient shapes payload written to the client, fromClient shapes payload read from it.
	toClient   *bucket
	fromClient *bucket

	limit   uint64           // bytes, 0 = unlimited
	used    atomic.Uint64    // wire bytes this month, counted against limit
	traffic [2]atomic.Uint64 // wire bytes since start, by direction
	kicked  atomic.Bool

	mu    sync.Mutex
	conns map[*WireConn]struct{}
}

func NewRegistry(cfgs []config.UserConfig) (*Registry, error) {
	if len(cfgs) == 0 {
		return nil, nil
	}
	r := &Registry{exact: make(map[string]*User, len(cfgs))}
	for i, c := range cfgs {
		hash, _, err := config.UserHashFromKey(c.Key)
		if err != nil {
			return nil, fmt.Errorf("users[%d]: %w", i, err)
		}
		alias := c.Alias
		if alias == "" {
			alias = hash
		}
		u := &User{
			Alias:   alias,
			Blocked: c.Blocked,
			prefix:  hash,
			limit:   uint64(c.Data * bytesPerGB),
			conns:   make(map[*WireConn]struct{}),
		}
		if len(c.Bandwidth) == 2 {
			u.toClient = newBucket(c.Bandwidth[0])
			u.fromClient = newBucket(c.Bandwidth[1])
		}
		r.all = append(r.all, u)
		if len(hash) == config.UserHashHexLen {
			r.exact[hash] = u
		} else {
			r.prefixed = append(r.prefixed, u)
		}
	}
	return r, nil
}

// Lookup returns the user for a hex handshake hash, or nil when it is not whitelisted.
func (r *Registry) Lookup(hash string) *User {
	if r == nil {
		return nil
	}
	if u, ok := r.exact[hash]; ok {
		return u
	}
	for _, u := range r.prefixed {
		if len(hash) >= len(u.prefix) && hash[:len(u.prefix)] == u.prefix {
			return u
		}
	}
	return nil
}

// Exceeded reports whether the user's data quota is used up.
func (u *User) Exceeded() bool {
	return u.limit > 0 && u.used.Load() >= u.limit
}

func (u *User) addTraffic(dir int, n uint64) {
	if n == 0 {
		return
	}
	u.traffic[dir].Add(n)
	total := u.used.Add(n)
	if u.limit == 0 || total < u.limit {
		return
	}
	if u.kicked.CompareAndSwap(false, true) {
		logx.Warnf("Server", "[User:%s] data quota exceeded, closing connections", u.Alias)
	}
	u.mu.Lock()
	conns := make([]*WireConn, 0, len(u.conns))
	for c := range u.conns {
		conns = append(conns, c)
	}
	u.mu.Unlock()
	for _, c := range conns {
		_ = c.Conn.Close()
	}
}

// Stat is a point-in-time view of one user, for monitoring.
type Stat struct {
	Name     string
	Blocked  bool
	Exceeded bool
	Conns    int    // open connections
	Up, Down uint64 // wire bytes from and to the client since start
	Used     uint64 // wire bytes this month, counted against Limit
	Limit    uint64 // bytes, 0 = unlimited
}

// Stats lists every user in config order.
func (r *Registry) Stats() []Stat {
	if r == nil {
		return nil
	}
	stats := make([]Stat, len(r.all))
	for i, u := range r.all {
		u.mu.Lock()
		conns := len(u.conns)
		u.mu.Unlock()
		stats[i] = Stat{
			Name:     u.Alias,
			Blocked:  u.Blocked,
			Exceeded: u.Exceeded(),
			Conns:    conns,
			Up:       u.traffic[upload].Load(),
			Down:     u.traffic[download].Load(),
			Used:     u.used.Load(),
			Limit:    u.limit,
		}
	}
	return stats
}

// WrapPayload throttles decoded payload to the user's bandwidth limits.
// It returns conn unchanged when the user has no limit.
func (u *User) WrapPayload(conn net.Conn) net.Conn {
	if u.toClient == nil && u.fromClient == nil {
		return conn
	}
	return &throttledConn{Conn: conn, wr: u.toClient, rd: u.fromClient, done: make(chan struct{})}
}

// WrapWire counts every byte on conn (both directions) against the user's data quota.
func (u *User) WrapWire(conn net.Conn) net.Conn {
	w := NewWireConn(conn)
	w.Bind(u)
	return w
}

// WireConn counts wire bytes. Bytes seen before Bind (the handshake) are charged on Bind.
type WireConn struct {
	net.Conn
	user    atomic.Pointer[User]
	pending [2]atomic.Uint64 // by direction
}

func NewWireConn(conn net.Conn) *WireConn { return &WireConn{Conn: conn} }

func (w *WireConn) Bind(u *User) {
	u.mu.Lock()
	u.conns[w] = struct{}{}
	u.mu.Unlock()
	w.user.Store(u)
	u.addTraffic(upload, w.pending[upload].Swap(0))
	u.addTraffic(download, w.pending[download].Swap(0))
}

func (w *WireConn) count(dir int, n int) {
	if n <= 0 {
		return
	}
	if u := w.user.Load(); u != nil {
		u.addTraffic(dir, uint64(n))
		return
	}
	w.pending[dir].Add(uint64(n))
}

func (w *WireConn) Read(p []byte) (int, error) {
	n, err := w.Conn.Read(p)
	w.count(upload, n)
	return n, err
}

func (w *WireConn) Write(p []byte) (int, error) {
	n, err := w.Conn.Write(p)
	w.count(download, n)
	return n, err
}

func (w *WireConn) Close() error {
	if u := w.user.Load(); u != nil {
		u.mu.Lock()
		delete(u.conns, w)
		u.mu.Unlock()
	}
	return w.Conn.Close()
}

func (w *WireConn) CloseWrite() error { return connutil.TryCloseWrite(w.Conn) }
func (w *WireConn) CloseRead() error  { return connutil.TryCloseRead(w.Conn) }

// bucket is a token bucket allowing debt, so large writes never need to fit the burst.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	tokens float64
	last   time.Time
	chunk  int
}

func newBucket(mbps float64) *bucket {
	if mbps <= 0 {
		return nil
	}
	rate := mbps * 1e6 / 8
	chunk := int(rate / 20) // ~50ms of traffic
	chunk = min(max(chunk, 2048), 64*1024)
	return &bucket{rate: rate, tokens: float64(chunk), last: time.Now(), chunk: chunk}
}

func (b *bucket) reserve(n int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(b.tokens+now.Sub(b.last).Seconds()*b.rate, float64(b.chunk))
	b.last = now
	b.tokens -= float64(n)
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / b.rate * float64(time.Second))
}

type throttledConn struct {
	net.Conn
	wr, rd *bucket
	done   chan struct{}
	once   sync.Once
}

func (c *throttledConn) wait(d time.Duration) error {
	if d < time.Millisecond {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-c.done:
		return net.ErrClosed
	}
}

func (c *throttledConn) Read(p []byte) (int, error) {
	if c.rd == nil {
		return c.Conn.Read(p)
	}
	if len(p) > c.rd.chunk {
		p = p[:c.rd.chunk]
	}
	n, err := c.Conn.Read(p)
	if n > 0 {
		if werr := c.wait(c.rd.reserve(n)); werr != nil {
			return 0, werr
		}
	}
	return n, err
}

func (c *throttledConn) Write(p []byte) (int, error) {
	if c.wr == nil {
		return c.Conn.Write(p)
	}
	total := 0
	for len(p) > 0 {
		n := min(len(p), c.wr.chunk)
		if err := c.wait(c.wr.reserve(n)); err != nil {
			return total, err
		}
		w, err := c.Conn.Write(p[:n])
		total += w
		if err != nil {
			return total, err
		}
		p = p[n:]
	}
	return total, nil
}

func (c *throttledConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}

func (c *throttledConn) CloseWrite() error { return connutil.TryCloseWrite(c.Conn) }
func (c *throttledConn) CloseRead() error  { return connutil.TryCloseRead(c.Conn) }

// SudokuUplinkPacked forwards the obfs metadata the reverse-proxy session checks.
func (c *throttledConn) SudokuUplinkPacked() bool {
	v, ok := c.Conn.(interface{ SudokuUplinkPacked() bool })
	return ok && v.SudokuUplinkPacked()
}
