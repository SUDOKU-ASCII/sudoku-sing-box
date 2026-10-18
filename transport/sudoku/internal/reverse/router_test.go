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
package reverse

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/transport/sudoku/internal/reverse/rewrite"
)

func TestPathPrefixMatch(t *testing.T) {
	tests := []struct {
		path, prefix string
		want         bool
	}{
		{"/a", "", false},
		{"", "", false},
		{"/anything", "/", true},
		{"", "/", true},
		{"/a", "/a", true},
		{"/a/", "/a", true},
		{"/a/b", "/a", true},
		{"/ab", "/a", false},
		{"/b", "/a", false},
		{"", "/a", false},
		{"/a", "/a/b", false},
	}
	for _, tc := range tests {
		if got := pathPrefixMatch(tc.path, tc.prefix); got != tc.want {
			t.Errorf("pathPrefixMatch(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.want)
		}
	}
}

func TestMatchRouteFallbacks(t *testing.T) {
	app := &routeEntry{prefix: "/app"}
	appAdmin := &routeEntry{prefix: "/app/admin"}
	other := &routeEntry{prefix: "/other"}
	m := &Manager{routes: map[string]*routeEntry{"/app": app, "/app/admin": appAdmin, "/other": other}}

	t.Run("longest prefix wins", func(t *testing.T) {
		tests := []struct {
			path string
			want *routeEntry
		}{
			{"/app", app},
			{"/app/x", app},
			{"/app/admin", appAdmin},
			{"/app/admin/users", appAdmin},
			{"/app/administrator", app},
			{"/other/x", other},
			{"/appx", nil},
			{"/", nil},
		}
		for _, tc := range tests {
			if got := m.matchLocked(tc.path); got != tc.want {
				t.Errorf("matchLocked(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		}
	})

	t.Run("referer", func(t *testing.T) {
		tests := []struct {
			referer string
			want    *routeEntry
		}{
			{"http://front/app/page?x=1", app},
			{"http://front/other", other},
			{"http://front/app/admin/", appAdmin},
			{"http://front", nil},
			{"http://front/unknown/page", nil},
			{"", nil},
			{"://bad", nil},
		}
		for _, tc := range tests {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.referer != "" {
				r.Header.Set("Referer", tc.referer)
			}
			if got := m.matchByRefererLocked(r); got != tc.want {
				t.Errorf("matchByRefererLocked(%q) = %+v, want %+v", tc.referer, got, tc.want)
			}
		}
	})

	t.Run("cookie", func(t *testing.T) {
		tests := []struct {
			cookie string
			want   *routeEntry
		}{
			{"%2Fapp", app},
			{"/app", app},
			{"app", app},
			{"%2Fapp%2Fadmin", appAdmin},
			{"%2Fother%2F", other},
			{"%2Funknown", nil},
			{"%zz", nil},
			{"%2F", nil},
			{"", nil},
		}
		for _, tc := range tests {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.AddCookie(&http.Cookie{Name: rewrite.PrefixCookieName, Value: tc.cookie})
			if got := m.matchByCookieLocked(r); got != tc.want {
				t.Errorf("matchByCookieLocked(%q) = %+v, want %+v", tc.cookie, got, tc.want)
			}
		}
		if got := m.matchByCookieLocked(httptest.NewRequest(http.MethodGet, "/x", nil)); got != nil {
			t.Errorf("no cookie matched %+v", got)
		}
	})
}

// stubProxy records the path the Manager hands to a route and answers 200 "proxied".
func stubProxy(seenPath *string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			*seenPath = r.URL.Path
			r.URL.Scheme = "http"
			r.URL.Host = "stub"
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("proxied")),
				Request:    r,
			}, nil
		}),
	}
}

func TestManagerServeHTTPRouting(t *testing.T) {
	type routed struct {
		code     int
		location string
		body     string
		seenPath string
	}
	const (
		proxied  = "proxied"
		notFound = "404 page not found\n"
	)

	tests := []struct {
		name    string
		method  string
		target  string
		headers map[string]string
		want    routed
	}{
		{"prefixed path is proxied", "GET", "/app/x", nil, routed{200, "", proxied, "/app/x"}},
		{"directory root is proxied", "GET", "/app/", nil, routed{200, "", proxied, "/app/"}},
		{"GET on bare prefix redirects", "GET", "/app", nil, routed{308, "/app/", "", ""}},
		{"HEAD on bare prefix redirects", "HEAD", "/app", nil, routed{308, "/app/", "", ""}},
		{"redirect keeps the query", "GET", "/app?a=1&b=2", nil, routed{308, "/app/?a=1&b=2", "", ""}},
		{"POST on bare prefix is proxied", "POST", "/app", nil, routed{200, "", proxied, "/app"}},
		{"websocket upgrade without subprotocol is proxied, not redirected", "GET", "/app",
			map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}, routed{200, "", proxied, "/app"}},
		{"unknown path is 404", "GET", "/nothing", nil, routed{404, "", notFound, ""}},
		{"referer routes root-absolute asset", "GET", "/static/app.js",
			map[string]string{"Referer": "http://front/app/page"}, routed{200, "", proxied, "/app/static/app.js"}},
		{"referer routes root-absolute asset onto other prefix", "GET", "/static/app.js",
			map[string]string{"Referer": "http://front/other/page"}, routed{200, "", proxied, "/other/static/app.js"}},
		{"cookie routes root-absolute asset", "GET", "/static/app.js",
			map[string]string{"Cookie": "sudoku_rev_prefix=%2Fother"}, routed{200, "", proxied, "/other/static/app.js"}},
		{"referer wins over cookie", "GET", "/static/app.js",
			map[string]string{"Referer": "http://front/app/", "Cookie": "sudoku_rev_prefix=%2Fother"}, routed{200, "", proxied, "/app/static/app.js"}},
		{"referer without route falls back to cookie", "GET", "/static/app.js",
			map[string]string{"Referer": "http://front/unknown/", "Cookie": "sudoku_rev_prefix=%2Fapp"}, routed{200, "", proxied, "/app/static/app.js"}},
		{"unmatched referer and cookie is 404", "GET", "/static/app.js",
			map[string]string{"Referer": "http://front/unknown/", "Cookie": "sudoku_rev_prefix=%2Funknown"}, routed{404, "", notFound, ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var appPath, otherPath string
			m := &Manager{routes: map[string]*routeEntry{
				"/app":   {prefix: "/app", proxy: stubProxy(&appPath)},
				"/other": {prefix: "/other", proxy: stubProxy(&otherPath)},
			}}
			req := httptest.NewRequest(tc.method, tc.target, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			m.ServeHTTP(rec, req)

			seen := appPath + otherPath
			got := routed{rec.Code, rec.Header().Get("Location"), rec.Body.String(), seen}
			if tc.method == "HEAD" || got.code == http.StatusPermanentRedirect {
				got.body = ""
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("root route neither redirects nor rewrites", func(t *testing.T) {
		var seen string
		m := &Manager{routes: map[string]*routeEntry{"/": {prefix: "/", proxy: stubProxy(&seen)}}}
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything/else", nil))
		if rec.Code != http.StatusOK || seen != "/anything/else" {
			t.Fatalf("code=%d seen=%q", rec.Code, seen)
		}
	})
}
