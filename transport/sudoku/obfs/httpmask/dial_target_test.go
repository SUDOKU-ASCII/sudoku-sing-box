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
package httpmask

import "testing"

func TestNormalizeDialTargets(t *testing.T) {
	type target struct{ scheme, urlHost, dialAddr, serverName string }
	cases := []struct {
		name     string
		ws       bool
		address  string
		tls      bool
		override string
		want     target
		wantErr  bool
	}{
		{name: "http plain", address: "1.2.3.4:8080", want: target{"http", "1.2.3.4:8080", "1.2.3.4:8080", "1.2.3.4"}},
		{name: "http tls", address: "example.com:443", tls: true, want: target{"https", "example.com:443", "example.com:443", "example.com"}},
		{name: "http override host", address: "1.2.3.4:443", tls: true, override: "cdn.example.com", want: target{"https", "cdn.example.com:443", "1.2.3.4:443", "cdn.example.com"}},
		{name: "http override host and port", address: "1.2.3.4:443", override: "cdn.example.com:8443", want: target{"http", "cdn.example.com:8443", "1.2.3.4:8443", "cdn.example.com"}},
		{name: "http ipv6", address: "[::1]:80", want: target{"http", "[::1]:80", "[::1]:80", "::1"}},
		{name: "http without port", address: "example.com", wantErr: true},
		{name: "ws plain", ws: true, address: "1.2.3.4:8080", want: target{"ws", "1.2.3.4:8080", "1.2.3.4:8080", "1.2.3.4"}},
		{name: "ws tls", ws: true, address: "example.com:443", tls: true, want: target{"wss", "example.com:443", "example.com:443", "example.com"}},
		{name: "ws scheme in address", ws: true, address: "wss://example.com:8443", want: target{"wss", "example.com:8443", "example.com:8443", "example.com"}},
		{name: "ws default port", ws: true, address: "example.com", want: target{"ws", "example.com:80", "example.com:80", "example.com"}},
		{name: "wss default port", ws: true, address: "wss://example.com", want: target{"wss", "example.com:443", "example.com:443", "example.com"}},
		{name: "ws override", ws: true, address: "1.2.3.4:443", tls: true, override: "cdn.example.com:8443", want: target{"wss", "cdn.example.com:8443", "1.2.3.4:8443", "cdn.example.com"}},
		{name: "ws malformed port", ws: true, address: "example.com:", wantErr: false, want: target{"ws", "example.com:", "example.com:", "example.com"}},
		{name: "ws bare ipv6", ws: true, address: "::1", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalize := normalizeHTTPDialTarget
			if tc.ws {
				normalize = normalizeWSDialTarget
			}
			scheme, urlHost, dialAddr, serverName, err := normalize(tc.address, tc.tls, tc.override)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got := (target{scheme, urlHost, dialAddr, serverName}); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
