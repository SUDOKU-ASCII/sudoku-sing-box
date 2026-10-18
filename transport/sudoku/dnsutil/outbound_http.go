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

// ClientResolver is the shared IPv4-only resolver for client-side lookups of public
// destinations such as PAC/geodata downloads.
var ClientResolver = sync.OnceValue(func() *Resolver {
	r, err := NewResolver(RecommendedClientOptions())
	if err != nil {
		panic("dnsutil: invalid built-in client options: " + err.Error())
	}
	return r
})

// NewOutboundHTTPClient returns a client that dials through OutboundDialer and
// resolves destinations with resolver, falling back to the dialer's own lookup.
func NewOutboundHTTPClient(timeout time.Duration, resolver *Resolver) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	dialer := OutboundDialer(timeout)
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if resolved, err := resolver.Resolve(ctx, addr); err == nil {
			addr = resolved
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}
