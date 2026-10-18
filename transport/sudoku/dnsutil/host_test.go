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

func TestParseIPLiteral(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1.2.3.4", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"2001:db8::1", true},
		{"::ffff:1.2.3.4", true},
		{"fe80::1%eth0", false},
		{"", false},
		{"example.com", false},
		{"1.2.3", false},
		{"1.2.3.4.", false},
		{"1.2.3.256", false},
		{"123", false},
		{"1.2.3.4:80", false},
		{"[::1]", false},
	}
	for _, tc := range tests {
		if _, ok := ParseIPLiteral(tc.in); ok != tc.want {
			t.Errorf("ParseIPLiteral(%q) = %v, want %v", tc.in, ok, tc.want)
		}
	}
}

func TestParseIPLiteralDoesNotAllocateForHostnames(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { ParseIPLiteral("www.example.com") }); n != 0 {
		t.Fatalf("hostname path allocated %v times per call", n)
	}
}

func TestNormalizeHost(t *testing.T) {
	tests := map[string]string{
		"Example.COM":   "example.com",
		" example.com ": "example.com",
		"example.com.":  "example.com",
		"":              "",
		".":             "",
	}
	for in, want := range tests {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsLikelyLocalHostname(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"printer", true},
		{"NAS.local", true},
		{"router.lan", true},
		{"localhost", true},
		{"host.localdomain", true},
		{"router.home.arpa", true},
		{"[nas]", true},
		{"", false},
		{"example.com", false},
		{"lan.example.com", false},
		{"mylan", true},
		{"foo.lan.com", false},
	}
	for _, tc := range tests {
		if got := isLikelyLocalHostname(tc.host); got != tc.want {
			t.Errorf("isLikelyLocalHostname(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
