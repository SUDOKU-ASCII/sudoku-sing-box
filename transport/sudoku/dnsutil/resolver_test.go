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
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

func fixedLookup(ss ...string) lookupIPFunc {
	answer := addrs(ss...)
	return func(context.Context, string, string) ([]netip.Addr, error) { return answer, nil }
}

func TestResolve_IPLiteralBypassDNS(t *testing.T) {
	r := newResolver(time.Minute, func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		t.Fatalf("DNS should not be called for IP literal")
		return nil, nil
	})

	addr, err := r.Resolve(context.Background(), "1.2.3.4:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "1.2.3.4:80" {
		t.Fatalf("unexpected addr: %s", addr)
	}
}

func TestResolve_CacheHitAvoidsDNS(t *testing.T) {
	var calls atomic.Int64
	lookup := func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		calls.Add(1)
		return addrs("1.2.3.4"), nil
	}

	r := newResolver(100*time.Millisecond, lookup)
	ctx := context.Background()

	addr1, err := r.Resolve(ctx, "example.com:80")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if addr1 != "1.2.3.4:80" {
		t.Fatalf("unexpected addr1: %s", addr1)
	}

	addr2, err := r.Resolve(ctx, "Example.COM.:80")
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if addr2 != addr1 {
		t.Fatalf("cache mismatch: %s vs %s", addr1, addr2)
	}

	if calls.Load() != 1 {
		t.Fatalf("expected a single IPv4 lookup, got %d", calls.Load())
	}
}

func TestResolve_OptimisticCacheOnFailure(t *testing.T) {
	var mu sync.Mutex
	fail := false

	lookup := func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return nil, fmt.Errorf("dns failure")
		}
		return addrs("1.2.3.4"), nil
	}

	r := newResolver(20*time.Millisecond, lookup)
	ctx := context.Background()

	addr1, err := r.Resolve(ctx, "example.com:80")
	if err != nil {
		t.Fatalf("initial resolve failed: %v", err)
	}
	expected := "1.2.3.4:80"
	if addr1 != expected {
		t.Fatalf("unexpected addr1: %s", addr1)
	}

	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	fail = true
	mu.Unlock()

	addr2, err := r.Resolve(ctx, "example.com:80")
	if err != nil {
		t.Fatalf("resolve with failing DNS should still succeed via optimistic cache: %v", err)
	}
	if addr2 != expected {
		t.Fatalf("unexpected addr2 with optimistic cache: %s", addr2)
	}
}

func TestResolve_StaleEntryExpiresAfterMaxStaleAge(t *testing.T) {
	r := newResolver(time.Minute, func(context.Context, string, string) ([]netip.Addr, error) {
		return nil, errors.New("dns failure")
	})
	r.cache["old.example"] = cacheEntry{ip: "1.2.3.4", expiresAt: time.Now().Add(-maxStaleAge - time.Minute)}
	r.cache["recent.example"] = cacheEntry{ip: "5.6.7.8", expiresAt: time.Now().Add(-time.Minute)}

	if got, err := r.Resolve(context.Background(), "recent.example:80"); err != nil || got != "5.6.7.8:80" {
		t.Fatalf("recent stale entry: got %q, %v", got, err)
	}
	if _, err := r.Resolve(context.Background(), "old.example:80"); err == nil {
		t.Fatal("entry older than maxStaleAge must not be served")
	}
}

func TestResolve_InvalidAddress(t *testing.T) {
	r := newResolver(time.Minute, nil)
	for _, addr := range []string{"", "bad-address"} {
		if _, err := r.Resolve(context.Background(), addr); err == nil {
			t.Fatalf("expected error for %q", addr)
		}
	}
}

func TestResolve_EmptyHost(t *testing.T) {
	r := newResolver(time.Minute, fixedLookup("1.2.3.4"))
	if _, err := r.Resolve(context.Background(), ":80"); !errors.Is(err, errEmptyHost) {
		t.Fatalf("err = %v, want %v", err, errEmptyHost)
	}
}

func TestResolve_LiteralWithTrailingDotSkipsLookup(t *testing.T) {
	r := newResolver(time.Minute, func(context.Context, string, string) ([]netip.Addr, error) {
		t.Fatal("DNS should not be called for IP literal")
		return nil, nil
	})
	got, err := r.Resolve(context.Background(), "1.2.3.4.:80")
	if err != nil || got != "1.2.3.4:80" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestQueryServers_IPv4Only(t *testing.T) {
	r := newResolver(time.Minute, func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		if network != "ip4" {
			return nil, fmt.Errorf("unexpected network: %s", network)
		}
		return addrs("1.2.3.4"), nil
	})
	ip, err := r.queryServers(context.Background(), "example.com")
	if err != nil || ip != "1.2.3.4" {
		t.Fatalf("got %q, %v", ip, err)
	}
}

func TestQueryServers_FiltersBogusBenchIPs(t *testing.T) {
	r := newResolver(time.Minute, fixedLookup("198.18.0.1", "198.19.255.254", "1.2.3.4"))
	r.bogusNets = []netip.Prefix{benchmarkNet}

	ip, err := r.queryServers(context.Background(), "example.com")
	if err != nil || ip != "1.2.3.4" {
		t.Fatalf("got %q, %v, want the first non-bogus address", ip, err)
	}
}

func TestQueryServers_BogusOnlyFallsThroughToNextServer(t *testing.T) {
	r := newResolver(time.Minute, fixedLookup("198.18.0.1"))
	r.bogusNets = []netip.Prefix{benchmarkNet}
	next := fixedLookup("8.8.4.4")
	r.servers = append(r.servers, localNameServer{lookup: next})

	ip, err := r.queryServers(context.Background(), "example.com")
	if err != nil || ip != "8.8.4.4" {
		t.Fatalf("got %q, %v", ip, err)
	}

	r.servers = r.servers[:1]
	if _, err := r.queryServers(context.Background(), "example.com"); !errors.Is(err, errNoIPRecords) {
		t.Fatalf("err = %v, want %v", err, errNoIPRecords)
	}
}

func TestQueryServers_ReportsFirstError(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	fail := func(err error) lookupIPFunc {
		return func(context.Context, string, string) ([]netip.Addr, error) { return nil, err }
	}
	r := newResolver(time.Minute, fail(first))
	r.servers = append(r.servers, localNameServer{lookup: fail(second)})
	if _, err := r.queryServers(context.Background(), "example.com"); !errors.Is(err, first) {
		t.Fatalf("err = %v, want %v", err, first)
	}
}

func TestResolve_ConcurrentMissesShareOneQuery(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	r := newResolver(time.Minute, func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		calls.Add(1)
		<-release
		return addrs("1.2.3.4"), nil
	})

	const callers = 16
	results := make(chan string, callers)
	for i := 0; i < callers; i++ {
		go func() {
			got, err := r.Resolve(context.Background(), "example.com:80")
			if err != nil {
				got = err.Error()
			}
			results <- got
		}()
	}
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for i := 0; i < callers; i++ {
		if got := <-results; got != "1.2.3.4:80" {
			t.Fatalf("caller got %q", got)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("server queried %d times, want 1", n)
	}
}

func TestResolve_CallerCancelDoesNotAbortSharedQuery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var queryErr atomic.Value
	r := newResolver(time.Minute, func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			queryErr.Store(err)
		}
		return addrs("1.2.3.4"), nil
	})

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := r.Resolve(leaderCtx, "example.com:80")
		leaderDone <- err
	}()
	<-started

	followerDone := make(chan string, 1)
	go func() {
		got, err := r.Resolve(context.Background(), "example.com:80")
		if err != nil {
			got = err.Error()
		}
		followerDone <- got
	}()
	time.Sleep(20 * time.Millisecond)

	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader err = %v, want context.Canceled", err)
	}
	close(release)
	if got := <-followerDone; got != "1.2.3.4:80" {
		t.Fatalf("follower got %q", got)
	}
	if err := queryErr.Load(); err != nil {
		t.Fatalf("shared query was cancelled: %v", err)
	}
}

func TestResolve_CacheIsBounded(t *testing.T) {
	r := newResolver(time.Minute, fixedLookup("1.2.3.4"))
	for i := 0; i < maxCacheEntries+100; i++ {
		if _, err := r.Resolve(context.Background(), "host"+strconv.Itoa(i)+".example:80"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.cache); n != maxCacheEntries {
		t.Fatalf("cache holds %d entries, want %d", n, maxCacheEntries)
	}
	if len(r.inflight) != 0 {
		t.Fatalf("%d lookups left in flight", len(r.inflight))
	}
}

func TestPutLocked_EvictsHopelessEntriesBeforeLiveOnes(t *testing.T) {
	r := newResolver(time.Minute, fixedLookup("1.2.3.4"))
	now := time.Now()
	for i := 0; i < maxCacheEntries; i++ {
		r.cache["live"+strconv.Itoa(i)] = cacheEntry{ip: "1.1.1.1", expiresAt: now.Add(time.Hour)}
	}
	r.cache["dead"] = cacheEntry{ip: "2.2.2.2", expiresAt: now.Add(-2 * maxStaleAge)}

	r.putLocked("new", cacheEntry{ip: "3.3.3.3", expiresAt: now.Add(time.Hour)}, now)

	if _, ok := r.cache["dead"]; ok {
		t.Fatal("entry past maxStaleAge should have been evicted")
	}
	if _, ok := r.cache["new"]; !ok {
		t.Fatal("new entry missing")
	}
	if n := len(r.cache); n != maxCacheEntries {
		t.Fatalf("cache holds %d entries, want %d", n, maxCacheEntries)
	}
}
