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
	"net/netip"
	"testing"
	"time"
)

func benchResolver(b *testing.B) *Resolver {
	b.Helper()
	r := newResolver(time.Hour, func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("93.184.216.35")}, nil
	})
	if _, err := r.Resolve(context.Background(), "example.com:443"); err != nil {
		b.Fatal(err)
	}
	return r
}

func BenchmarkResolveHit(b *testing.B) {
	r := benchResolver(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Resolve(ctx, "Example.com:443"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveHitParallel(b *testing.B) {
	r := benchResolver(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := r.Resolve(ctx, "example.com:443"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkResolveIPLiteral(b *testing.B) {
	r := benchResolver(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Resolve(ctx, "203.0.113.9:443"); err != nil {
			b.Fatal(err)
		}
	}
}
