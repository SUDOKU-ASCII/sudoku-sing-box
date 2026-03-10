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
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	recommendedClientResolverOnce sync.Once
	recommendedClientResolver     *Resolver
)

// RecommendedClientResolver returns the built-in IPv4-only resolver used for
// client-side public destination lookups such as PAC/geodata downloads.
func RecommendedClientResolver() *Resolver {
	recommendedClientResolverOnce.Do(func() {
		r, err := NewResolver(RecommendedClientOptions())
		if err != nil {
			recommendedClientResolver = newResolver(defaultCacheTTL, nil)
			return
		}
		recommendedClientResolver = r
	})
	if recommendedClientResolver == nil {
		return newResolver(defaultCacheTTL, nil)
	}
	return recommendedClientResolver
}

func NewOutboundHTTPClient(timeout time.Duration, resolver *Resolver) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewOutboundHTTPTransport(timeout, resolver),
	}
}

func NewOutboundHTTPTransport(timeout time.Duration, resolver *Resolver) *http.Transport {
	tr := cloneDefaultHTTPTransport()
	dialer := OutboundDialer(timeout)
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		target := addr
		if resolver != nil {
			if resolved, err := resolver.Resolve(ctx, addr); err == nil && resolved != "" {
				target = resolved
			}
		}
		return dialer.DialContext(ctx, network, target)
	}
	return tr
}

func cloneDefaultHTTPTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		return base.Clone()
	}
	return &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: true,
	}
}
