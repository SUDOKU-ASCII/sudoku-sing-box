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
package dnsutil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	maxCacheEntries = 4096
	// maxStaleAge bounds how long past its TTL an entry may still answer when every server fails.
	maxStaleAge = 24 * time.Hour
)

// benchmarkNet is the range fake-IP DNS servers answer with; real lookups never return it.
var benchmarkNet = netip.MustParsePrefix("198.18.0.0/15")

var (
	errNoIPRecords = errors.New("no ip records found")
	errEmptyHost   = errors.New("empty host")
)

// lookupIPFunc has the shape of net.Resolver.LookupNetIP.
type lookupIPFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

var systemLookup lookupIPFunc = net.DefaultResolver.LookupNetIP

type nameServer interface {
	LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error)
}

type localNameServer struct {
	lookup lookupIPFunc
}

func (s localNameServer) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	return s.lookup(ctx, "ip4", host)
}

// cacheEntry keeps only the address Resolve hands out, already rendered.
type cacheEntry struct {
	ip        string
	expiresAt time.Time
}

// lookupCall is one in-flight server query shared by every caller asking for the same host.
type lookupCall struct {
	done  chan struct{}
	entry cacheEntry
	err   error
}

type Resolver struct {
	mu        sync.RWMutex
	cache     map[string]cacheEntry
	inflight  map[string]*lookupCall
	ttl       time.Duration
	timeout   time.Duration
	bogusNets []netip.Prefix
	servers   []nameServer
}

var defaultResolver = newResolver(defaultCacheTTL, nil)

func NewResolver(opts Options) (*Resolver, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultLookupTimeout
	}
	servers, err := buildNameServers(opts.Servers, timeout)
	if err != nil {
		return nil, err
	}
	r := newResolver(defaultCacheTTL, nil)
	r.timeout = timeout
	r.bogusNets = []netip.Prefix{benchmarkNet}
	r.servers = servers
	return r, nil
}

func newResolver(ttl time.Duration, lookup lookupIPFunc) *Resolver {
	if lookup == nil {
		lookup = systemLookup
	}
	return &Resolver{
		cache:    make(map[string]cacheEntry),
		inflight: make(map[string]*lookupCall),
		ttl:      ttl,
		timeout:  defaultLookupTimeout,
		servers:  []nameServer{localNameServer{lookup: lookup}},
	}
}

func buildNameServers(specs []ServerOptions, timeout time.Duration) ([]nameServer, error) {
	if len(specs) == 0 {
		return []nameServer{localNameServer{lookup: systemLookup}}, nil
	}
	servers := make([]nameServer, 0, len(specs))
	for _, spec := range specs {
		switch spec.Type {
		case serverTypeLocal:
			servers = append(servers, localNameServer{lookup: systemLookup})
		case serverTypeDoH:
			ns, err := newHTTPSNameServer(spec, timeout)
			if err != nil {
				return nil, err
			}
			servers = append(servers, ns)
		default:
			return nil, fmt.Errorf("unsupported dns server type %q", spec.Type)
		}
	}
	return servers, nil
}

// Resolve resolves addr with the process-wide system-DNS resolver.
func Resolve(ctx context.Context, addr string) (string, error) {
	return defaultResolver.Resolve(ctx, addr)
}

// Resolve replaces the host of a host:port address with its first IPv4 address.
func (r *Resolver) Resolve(ctx context.Context, addr string) (string, error) {
	if addr == "" {
		return "", errors.New("empty address")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if _, ok := ParseIPLiteral(host); ok {
		return addr, nil
	}
	ip, err := r.lookupHost(ctx, host)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip, port), nil
}

// lookupHost returns the address to dial for host. An expired entry still answers
// when refreshing fails, until it is maxStaleAge past its TTL.
func (r *Resolver) lookupHost(ctx context.Context, host string) (string, error) {
	key := normalizeHost(host)
	if key == "" {
		return "", errEmptyHost
	}
	if literal, ok := ParseIPLiteral(key); ok {
		return literal.Unmap().String(), nil
	}

	now := time.Now()
	r.mu.RLock()
	entry, cached := r.cache[key]
	r.mu.RUnlock()
	if cached && !now.After(entry.expiresAt) {
		return entry.ip, nil
	}

	fresh, err := r.refresh(ctx, key)
	if err == nil {
		return fresh.ip, nil
	}
	if cached && now.Before(entry.expiresAt.Add(maxStaleAge)) {
		return entry.ip, nil
	}
	return "", fmt.Errorf("dns lookup failed for %s: %w", key, err)
}

// refresh joins the in-flight query for key or starts one. The query outlives the
// caller that started it, so one caller giving up does not fail the others.
func (r *Resolver) refresh(ctx context.Context, key string) (cacheEntry, error) {
	r.mu.Lock()
	call, running := r.inflight[key]
	if !running {
		call = &lookupCall{done: make(chan struct{})}
		r.inflight[key] = call
	}
	r.mu.Unlock()

	if !running {
		queryCtx, cancel := r.queryContext(ctx)
		go r.runQuery(queryCtx, cancel, key, call)
	}
	select {
	case <-call.done:
		return call.entry, call.err
	case <-ctx.Done():
		return cacheEntry{}, ctx.Err()
	}
}

// queryContext keeps the caller's values and deadline but not its cancellation.
func (r *Resolver) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return context.WithTimeout(detached, r.timeout)
}

func (r *Resolver) runQuery(ctx context.Context, cancel context.CancelFunc, key string, call *lookupCall) {
	defer cancel()
	ip, err := r.queryServers(ctx, key)

	r.mu.Lock()
	if err == nil {
		now := time.Now()
		call.entry = cacheEntry{ip: ip, expiresAt: now.Add(r.ttl)}
		r.putLocked(key, call.entry, now)
	}
	call.err = err
	delete(r.inflight, key)
	r.mu.Unlock()
	close(call.done)
}

// putLocked inserts entry, first making room by dropping entries too stale to ever
// be served again and then, if still full, the one expiring soonest.
func (r *Resolver) putLocked(key string, entry cacheEntry, now time.Time) {
	if _, exists := r.cache[key]; !exists && len(r.cache) >= maxCacheEntries {
		var oldestKey string
		var oldest time.Time
		for k, e := range r.cache {
			if now.After(e.expiresAt.Add(maxStaleAge)) {
				delete(r.cache, k)
			} else if oldestKey == "" || e.expiresAt.Before(oldest) {
				oldestKey, oldest = k, e.expiresAt
			}
		}
		if len(r.cache) >= maxCacheEntries {
			delete(r.cache, oldestKey)
		}
	}
	r.cache[key] = entry
}

// queryServers asks the servers in order and returns the first usable address.
func (r *Resolver) queryServers(ctx context.Context, host string) (string, error) {
	var firstErr error
	for _, srv := range r.servers {
		addrs, err := srv.LookupIPv4(ctx, host)
		if err == nil && len(addrs) == 0 {
			err = errNoIPRecords
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if addr, ok := r.firstUsable(addrs); ok {
			return addr.String(), nil
		}
	}
	if firstErr == nil {
		firstErr = errNoIPRecords
	}
	return "", firstErr
}

// firstUsable returns the first valid address that is not in a bogus range.
func (r *Resolver) firstUsable(addrs []netip.Addr) (netip.Addr, bool) {
	for _, addr := range addrs {
		addr = addr.Unmap()
		if addr.IsValid() && !r.isBogus(addr) {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

func (r *Resolver) isBogus(addr netip.Addr) bool {
	for _, prefix := range r.bogusNets {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
