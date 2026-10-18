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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStripPathPrefix(t *testing.T) {
	tests := []struct {
		path, prefix, want string
	}{
		{"/p/x", "/p", "/x"},
		{"/p/x/y", "/p", "/x/y"},
		{"/p", "/p", "/"},
		{"/p/", "/p", "/"},
		{"/px", "/p", "/px"},
		{"/other", "/p", "/other"},
		{"", "/p", "/"},
		{"/a", "/", "/a"},
		{"/a", "", "/a"},
		{"", "/", "/"},
		{"", "", "/"},
	}
	for _, tc := range tests {
		if got := stripPathPrefix(tc.path, tc.prefix); got != tc.want {
			t.Errorf("stripPathPrefix(%q, %q) = %q, want %q", tc.path, tc.prefix, got, tc.want)
		}
	}
}

func TestDefaultUpstreamHostHeader(t *testing.T) {
	tests := []struct{ target, want string }{
		{"app.internal:8080", "app.internal:8080"},
		{" app.internal:8080 ", "app.internal:8080"},
		{"localhost:80", "localhost:80"},
		{"10.0.0.5:80", ""},
		{"[::1]:80", ""},
		{"[fe80::1%eth0]:80", ""},
		{"noport", ""},
		{":80", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := defaultUpstreamHostHeader(tc.target); got != tc.want {
			t.Errorf("defaultUpstreamHostHeader(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// upstreamRecorder answers every proxied request with resp and keeps the request the
// upstream would have seen.
type upstreamRecorder struct {
	req  *http.Request
	resp func(*http.Request) (*http.Response, error)
}

func (u *upstreamRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	u.req = r
	if u.resp != nil {
		return u.resp(r)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
}

func TestRouteProxyUpstreamRequest(t *testing.T) {
	type upstream struct {
		path, rawQuery, host, forwardedFor, forwardedPrefix, acceptEncoding string
		forwardedHost, forwardedProto                                       string
	}
	tests := []struct {
		name       string
		prefix     string
		target     string
		strip      bool
		hostHeader string
		want       upstream
	}{
		{"strip with domain target", "/app", "app.internal:8080", true, "",
			upstream{"/x", "y=1", "app.internal:8080", "198.51.100.1, 203.0.113.9", "/app", "", "", ""}},
		{"strip with explicit host header", "/app", "app.internal:8080", true, "custom.example",
			upstream{"/x", "y=1", "custom.example", "198.51.100.1, 203.0.113.9", "/app", "", "", ""}},
		{"strip with ip target keeps the client host", "/app", "10.0.0.5:8080", true, "",
			upstream{"/x", "y=1", "front.example", "198.51.100.1, 203.0.113.9", "/app", "", "", ""}},
		{"no strip keeps path and accept-encoding", "/app", "app.internal:8080", false, "",
			upstream{"/app/x", "y=1", "app.internal:8080", "198.51.100.1, 203.0.113.9", "/app", "gzip", "", ""}},
		{"root prefix is transparent", "/", "app.internal:8080", true, "",
			upstream{"/app/x", "y=1", "app.internal:8080", "198.51.100.1, 203.0.113.9", "", "gzip", "", ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := &upstreamRecorder{}
			rp := newRouteProxy(tc.prefix, tc.target, tc.strip, tc.hostHeader, nil)
			rp.Transport = up

			req := httptest.NewRequest(http.MethodGet, "http://front.example/app/x?y=1", nil)
			req.RemoteAddr = "203.0.113.9:5555"
			req.Header.Set("Accept-Encoding", "gzip")
			req.Header.Set("X-Forwarded-For", "198.51.100.1")
			rec := httptest.NewRecorder()
			rp.ServeHTTP(rec, req)

			if up.req == nil {
				t.Fatalf("upstream not reached: %d %s", rec.Code, rec.Body.String())
			}
			got := upstream{
				path:            up.req.URL.Path,
				rawQuery:        up.req.URL.RawQuery,
				host:            up.req.Host,
				forwardedFor:    up.req.Header.Get("X-Forwarded-For"),
				forwardedPrefix: up.req.Header.Get("X-Forwarded-Prefix"),
				acceptEncoding:  up.req.Header.Get("Accept-Encoding"),
				forwardedHost:   up.req.Header.Get("X-Forwarded-Host"),
				forwardedProto:  up.req.Header.Get("X-Forwarded-Proto"),
			}
			if got != tc.want {
				t.Fatalf("upstream request:\n got %+v\nwant %+v", got, tc.want)
			}
			if up.req.URL.Host != tc.target || up.req.URL.Scheme != "http" {
				t.Fatalf("upstream URL = %s://%s, want http://%s", up.req.URL.Scheme, up.req.URL.Host, tc.target)
			}
		})
	}

	t.Run("a client without remote address adds no forwarded-for", func(t *testing.T) {
		up := &upstreamRecorder{}
		rp := newRouteProxy("/app", "app.internal:8080", true, "", nil)
		rp.Transport = up
		req := httptest.NewRequest(http.MethodGet, "http://front.example/app/x", nil)
		req.RemoteAddr = ""
		rp.ServeHTTP(httptest.NewRecorder(), req)
		if up.req == nil || up.req.Header.Get("X-Forwarded-For") != "" {
			t.Fatalf("X-Forwarded-For = %q", up.req.Header.Get("X-Forwarded-For"))
		}
	})
}

func TestRouteProxyResponse(t *testing.T) {
	htmlResponse := func(r *http.Request) (*http.Response, error) {
		const body = `<a href="/x">go</a>`
		h := http.Header{}
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Location", "/login")
		h.Set("ETag", `"v1"`)
		h.Add("Set-Cookie", "sid=1; Path=/")
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        h,
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       r,
		}, nil
	}

	t.Run("stripped route restores the prefix everywhere", func(t *testing.T) {
		rp := newRouteProxy("/app", "app.internal:8080", true, "", nil)
		rp.Transport = &upstreamRecorder{resp: htmlResponse}
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://front.example/app/page", nil))

		const wantBody = `<a href="/app/x">go</a>`
		if rec.Body.String() != wantBody {
			t.Errorf("body = %q, want %q", rec.Body.String(), wantBody)
		}
		if got := rec.Header().Get("Location"); got != "/app/login" {
			t.Errorf("Location = %q", got)
		}
		if got := rec.Header().Get("ETag"); got != "" {
			t.Errorf("ETag must be dropped when the body changed, got %q", got)
		}
		if got := rec.Header().Get("Content-Length"); got != "23" {
			t.Errorf("Content-Length = %q, want 23", got)
		}
		wantCookies := []string{"sid=1;Path=/app/", "sudoku_rev_prefix=%2Fapp; Path=/; HttpOnly; SameSite=Lax"}
		if got := rec.Header().Values("Set-Cookie"); strings.Join(got, "\n") != strings.Join(wantCookies, "\n") {
			t.Errorf("Set-Cookie = %q, want %q", got, wantCookies)
		}
	})

	t.Run("route without strip leaves the response alone", func(t *testing.T) {
		rp := newRouteProxy("/app", "app.internal:8080", false, "", nil)
		rp.Transport = &upstreamRecorder{resp: htmlResponse}
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://front.example/app/page", nil))

		if rec.Body.String() != `<a href="/x">go</a>` || rec.Header().Get("Location") != "/login" ||
			rec.Header().Get("ETag") != `"v1"` || len(rec.Header().Values("Set-Cookie")) != 1 {
			t.Fatalf("response was modified: body=%q header=%v", rec.Body.String(), rec.Header())
		}
	})

	t.Run("upstream failure is a generic bad gateway", func(t *testing.T) {
		rp := newRouteProxy("/app", "app.internal:8080", true, "", nil)
		rp.Transport = &upstreamRecorder{resp: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("secret internal detail")
		}}
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://front.example/app/page", nil))
		if rec.Code != http.StatusBadGateway || rec.Body.String() != "bad gateway\n" {
			t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
		}
	})
}
