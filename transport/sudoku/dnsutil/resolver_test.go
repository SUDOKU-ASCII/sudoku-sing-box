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
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolve_IPLiteralBypassDNS(t *testing.T) {
	r := newResolver(1*time.Minute, func(ctx context.Context, network, host string) ([]net.IP, error) {
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
	lookup := func(ctx context.Context, network, host string) ([]net.IP, error) {
		calls.Add(1)
		return []net.IP{net.ParseIP("1.2.3.4")}, nil
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

	addr2, err := r.Resolve(ctx, "example.com:80")
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
	ip := net.ParseIP("1.2.3.4")
	if ip == nil {
		t.Fatalf("failed to parse test IP")
	}

	var mu sync.Mutex
	fail := false

	lookup := func(ctx context.Context, network, host string) ([]net.IP, error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return nil, fmt.Errorf("dns failure")
		}
		if network == "ip4" {
			return []net.IP{ip}, nil
		}
		// Simulate missing IPv6 record.
		return nil, fmt.Errorf("no ipv6")
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

	// Expire the cache entry.
	time.Sleep(30 * time.Millisecond)

	// Force DNS failure; resolver should still return cached IP.
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

func TestResolve_InvalidAddress(t *testing.T) {
	r := newResolver(1*time.Minute, nil)
	if _, err := r.Resolve(context.Background(), "bad-address"); err == nil {
		t.Fatalf("expected error for invalid address")
	}
}

func TestLookupIPs_IPv4Only(t *testing.T) {
	lookup := func(ctx context.Context, network, host string) ([]net.IP, error) {
		switch network {
		case "ip4":
			return []net.IP{net.ParseIP("1.2.3.4")}, nil
		default:
			return nil, fmt.Errorf("unexpected network: %s", network)
		}
	}

	r := newResolver(1*time.Minute, lookup)
	ips, err := r.LookupIPs(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if len(ips) != 1 {
		t.Fatalf("expected only IPv4 results, got %v", ips)
	}
	if ips[0].To4() == nil {
		t.Fatalf("expected IPv4 result, got %v", ips[0])
	}
}

func TestResolver_FiltersBogusBenchIPs(t *testing.T) {
	r := newResolver(1*time.Minute, func(ctx context.Context, network, host string) ([]net.IP, error) {
		return []net.IP{
			net.ParseIP("198.18.0.1"),
			net.ParseIP("1.2.3.4"),
		}, nil
	})
	_, benchNet, err := net.ParseCIDR("198.18.0.0/15")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	r.bogusNets = []*net.IPNet{benchNet}

	ips, err := r.LookupIPs(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "1.2.3.4" {
		t.Fatalf("unexpected filtered ips: %v", ips)
	}
}
