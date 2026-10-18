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
package rewrite

import (
	"net/http"
	"strings"
	"testing"
)

func TestNormalizePrefix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"/", ""},
		{"   ", ""},
		{"///", ""},
		{"/p", "/p"},
		{"p", "/p"},
		{"/p/", "/p"},
		{" /p// ", "/p"},
		{"/a/b/", "/a/b"},
	}
	for _, tc := range tests {
		if got := NormalizePrefix(tc.in); got != tc.want {
			t.Errorf("NormalizePrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRewriteLocation(t *testing.T) {
	tests := []struct {
		name, prefix, location, reqHost, want string
	}{
		{"root absolute", "/p", "/login", "front", "/p/login"},
		{"root absolute with query and fragment", "/p", "/login?next=%2Fa#f", "front", "/p/login?next=%2Fa#f"},
		{"root absolute keeps escaping", "/p", "/a%20b", "front", "/p/a%20b"},
		{"surrounding spaces trimmed", "/p", " /login ", "front", "/p/login"},
		{"already prefixed", "/p", "/p/login", "front", "/p/login"},
		{"exactly the prefix", "/p", "/p", "front", "/p"},
		{"shares only a string prefix", "/p", "/pp/x", "front", "/p/pp/x"},
		{"protocol relative untouched", "/p", "//cdn.example/x", "front", "//cdn.example/x"},
		{"relative untouched", "/p", "relative/path", "front", "relative/path"},
		{"empty untouched", "/p", "", "front", ""},
		{"same host http", "/p", "http://front/login", "front", "http://front/p/login"},
		{"same host case-insensitive", "/p", "http://FRONT/login", "front", "http://FRONT/p/login"},
		{"same host explicit port", "/p", "http://front:8080/login", "front:8080", "http://front:8080/p/login"},
		{"request host without port accepts any port", "/p", "http://front:9999/login", "front", "http://front:9999/p/login"},
		{"https default port", "/p", "https://front/x", "front:443", "https://front/p/x"},
		{"http default port mismatch", "/p", "http://front/x", "front:8080", "http://front/x"},
		{"other host untouched", "/p", "http://other/login", "front", "http://other/login"},
		{"absolute already prefixed", "/p", "http://front/p/login", "front", "http://front/p/login"},
		{"absolute exactly the prefix", "/p", "http://front/p", "front", "http://front/p"},
		{"absolute shares only a string prefix", "/p", "http://front/pp", "front", "http://front/p/pp"},
		{"absolute without path untouched", "/p", "http://front", "front", "http://front"},
		{"empty prefix untouched", "", "/login", "front", "/login"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				Header:  http.Header{},
				Request: &http.Request{Host: tc.reqHost},
			}
			if tc.location != "" {
				resp.Header.Set("Location", tc.location)
			}
			rewriteLocation(resp, tc.prefix)
			if got := resp.Header.Get("Location"); got != tc.want {
				t.Fatalf("Location = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRewriteSetCookiePath(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		in     []string
		want   []string
	}{
		{"root path", "/p", []string{"sid=1; Path=/"}, []string{"sid=1;Path=/p/"}},
		{"nested path", "/p", []string{"sid=1; Path=/a"}, []string{"sid=1;Path=/p/a"}},
		{"attribute name is case-insensitive", "/p", []string{"sid=1; path=/a"}, []string{"sid=1;Path=/p/a"}},
		{"other attributes keep their spacing", "/p", []string{"sid=1; HttpOnly; Path=/a; Secure"}, []string{"sid=1; HttpOnly;Path=/p/a; Secure"}},
		{"already prefixed", "/p", []string{"sid=1; Path=/p/x"}, []string{"sid=1; Path=/p/x"}},
		{"exactly the prefix", "/p", []string{"sid=1; Path=/p"}, []string{"sid=1; Path=/p"}},
		{"shares only a string prefix", "/p", []string{"sid=1; Path=/pp"}, []string{"sid=1;Path=/p/pp"}},
		{"protocol-relative-looking path untouched", "/p", []string{"sid=1; Path=//x"}, []string{"sid=1; Path=//x"}},
		{"relative path untouched", "/p", []string{"sid=1; Path=rel"}, []string{"sid=1; Path=rel"}},
		{"no path attribute", "/p", []string{"sid=1; HttpOnly"}, []string{"sid=1; HttpOnly"}},
		{"empty segment preserved", "/p", []string{"a=1;; Path=/x"}, []string{"a=1;;Path=/p/x"}},
		{"several cookies keep order", "/p", []string{"a=1; Path=/", "b=2", "c=3; Path=/c"}, []string{"a=1;Path=/p/", "b=2", "c=3;Path=/p/c"}},
		{"empty prefix untouched", "", []string{"sid=1; Path=/"}, []string{"sid=1; Path=/"}},
		{"root prefix untouched", "/", []string{"sid=1; Path=/"}, []string{"sid=1; Path=/"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Set-Cookie": append([]string(nil), tc.in...)}}
			rewriteSetCookiePath(resp, tc.prefix)
			got := resp.Header.Values("Set-Cookie")
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("Set-Cookie = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRewriteReversePrefixCookie(t *testing.T) {
	tests := []struct{ prefix, want string }{
		{"/p", "sudoku_rev_prefix=%2Fp; Path=/; HttpOnly; SameSite=Lax"},
		{"p/", "sudoku_rev_prefix=%2Fp; Path=/; HttpOnly; SameSite=Lax"},
		{"/a/b", "sudoku_rev_prefix=%2Fa%2Fb; Path=/; HttpOnly; SameSite=Lax"},
		{"", ""},
		{"/", ""},
	}
	for _, tc := range tests {
		resp := &http.Response{Header: http.Header{"Set-Cookie": {"keep=1"}}}
		rewriteReversePrefixCookie(resp, tc.prefix)
		got := resp.Header.Values("Set-Cookie")
		if tc.want == "" {
			if len(got) != 1 {
				t.Errorf("prefix %q: cookie added: %q", tc.prefix, got)
			}
			continue
		}
		if len(got) != 2 || got[0] != "keep=1" || got[1] != tc.want {
			t.Errorf("prefix %q: Set-Cookie = %q, want [keep=1 %s]", tc.prefix, got, tc.want)
		}
	}
}
