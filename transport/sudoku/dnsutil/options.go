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

import "time"

const (
	defaultCacheTTL      = 10 * time.Minute
	defaultLookupTimeout = 5 * time.Second

	serverTypeDoH   = "https"
	serverTypeLocal = "local"
)

// Options is the name-server policy of a Resolver; RecommendedClientOptions is its only producer.
type Options struct {
	Timeout time.Duration
	Servers []ServerOptions
}

type ServerOptions struct {
	Type      string   // serverTypeDoH or serverTypeLocal
	Address   string   // DoH host name; the port is always 443
	Bootstrap []string // DoH server IPs dialed instead of resolving Address
}

// RecommendedClientOptions is the built-in target-DNS policy used by the mixed proxy client.
func RecommendedClientOptions() Options {
	return Options{
		Timeout: 3 * time.Second,
		Servers: []ServerOptions{
			{Type: serverTypeDoH, Address: "dns.alidns.com", Bootstrap: []string{"223.5.5.5", "223.6.6.6"}},
			{Type: serverTypeDoH, Address: "doh.pub", Bootstrap: []string{"119.29.29.29", "119.28.28.28"}},
			{Type: serverTypeLocal},
		},
	}
}
