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
	"net/netip"
	"strings"
)

// normalizeHost returns the cache key for host.
func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// ParseIPLiteral parses s as an IP address without a zone. Names are rejected
// before netip.ParseAddr so the common hostname path allocates nothing.
func ParseIPLiteral(s string) (netip.Addr, bool) {
	if !mayBeIPLiteral(s) {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}
	return addr, true
}

// mayBeIPLiteral is a constant-time filter: a dotted quad starts and ends with a digit, IPv6 has a colon.
func mayBeIPLiteral(s string) bool {
	if s == "" {
		return false
	}
	if isDigit(s[0]) && isDigit(s[len(s)-1]) {
		return true
	}
	return strings.IndexByte(s, ':') >= 0
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// isLikelyLocalHostname reports names that only a local resolver can answer
// (single labels and the usual intranet suffixes).
func isLikelyLocalHostname(host string) bool {
	host = strings.ToLower(strings.TrimSpace(strings.Trim(host, "[]")))
	if host == "" {
		return false
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".localhost", ".localdomain", ".home.arpa", ".lan"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
