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

import "testing"

func TestShouldApplyOutboundControl(t *testing.T) {
	tests := []struct {
		name    string
		network string
		address string
		want    bool
	}{
		{name: "unix socket", network: "unix", address: "/tmp/sudoku.sock", want: false},
		{name: "localhost name", network: "tcp", address: "localhost:443", want: false},
		{name: "bare local hostname", network: "tcp", address: "router:443", want: false},
		{name: "dot local hostname", network: "tcp", address: "nas.local:443", want: false},
		{name: "loopback ipv4", network: "tcp", address: "127.0.0.1:443", want: false},
		{name: "loopback ipv6", network: "tcp", address: "[::1]:443", want: false},
		{name: "private ipv4", network: "tcp", address: "192.168.1.10:443", want: false},
		{name: "link local ipv4", network: "tcp", address: "169.254.10.20:443", want: false},
		{name: "multicast ipv4", network: "tcp", address: "224.0.0.1:443", want: false},
		{name: "unspecified ipv4", network: "tcp", address: "0.0.0.0:443", want: false},
		{name: "public domain", network: "tcp", address: "fastly.jsdelivr.net:443", want: true},
		{name: "public ipv4", network: "tcp", address: "1.1.1.1:443", want: true},
		{name: "public ipv6", network: "tcp", address: "[2001:4860:4860::8888]:443", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldApplyOutboundControl(tt.network, tt.address); got != tt.want {
				t.Fatalf("shouldApplyOutboundControl(%q, %q) = %v, want %v", tt.network, tt.address, got, tt.want)
			}
		})
	}
}
