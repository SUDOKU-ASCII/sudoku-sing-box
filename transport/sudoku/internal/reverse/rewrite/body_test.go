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
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func gzipBytes(t testing.TB, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func bodyResponse(path string, body []byte, header map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range header {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       httptest.NewRequest(http.MethodGet, "http://front"+path, nil),
	}
}

func TestRewriteTextBody(t *testing.T) {
	const prefix = "/p"
	gzChanged := gzipBytes(t, `<a href="/x">`)
	gzUnchanged := gzipBytes(t, `<a href="/p/x">`)

	tests := []struct {
		name       string
		path       string
		body       []byte
		header     map[string]string
		wantBody   string
		wantHeader map[string]string // "" means the header must be absent
	}{
		{
			name:     "html is rewritten and ETag dropped",
			path:     "/page",
			body:     []byte(`<a href="/x">`),
			header:   map[string]string{"Content-Type": "text/html", "ETag": `"v1"`, "Transfer-Encoding": "chunked"},
			wantBody: `<a href="/p/x">`,
			wantHeader: map[string]string{
				"Content-Length": "15", "ETag": "", "Transfer-Encoding": "",
			},
		},
		{
			name:     "html with charset parameter",
			path:     "/page",
			body:     []byte(`<a href="/x">`),
			header:   map[string]string{"Content-Type": "Text/HTML; charset=UTF-8"},
			wantBody: `<a href="/p/x">`,
		},
		{
			name:       "unchanged html keeps its ETag",
			path:       "/page",
			body:       []byte(`<a href="/p/x">`),
			header:     map[string]string{"Content-Type": "text/html", "ETag": `"v1"`},
			wantBody:   `<a href="/p/x">`,
			wantHeader: map[string]string{"ETag": `"v1"`, "Content-Length": "15"},
		},
		{
			name:     "html skips inline script and rewrites srcset",
			path:     "/page",
			body:     []byte(`<script>var a="/x";</script><img srcset="/a.png 1x, /b.png 2x">`),
			header:   map[string]string{"Content-Type": "text/html"},
			wantBody: `<script>var a="/x";</script><img srcset="/p/a.png 1x, /p/b.png 2x">`,
		},
		{
			name:     "xhtml takes the html path",
			path:     "/page",
			body:     []byte(`<script>"/x"</script>"/y"`),
			header:   map[string]string{"Content-Type": "application/xhtml+xml"},
			wantBody: `<script>"/x"</script>"/p/y"`,
		},
		{
			name:     "json",
			path:     "/api",
			body:     []byte(`{"u":"/a"}`),
			header:   map[string]string{"Content-Type": "application/json"},
			wantBody: `{"u":"/p/a"}`,
		},
		{
			name:     "css",
			path:     "/s.css",
			body:     []byte(`a{b:url(/i.png)}`),
			header:   map[string]string{"Content-Type": "text/css"},
			wantBody: `a{b:url(/p/i.png)}`,
		},
		{
			name:     "svg",
			path:     "/i.svg",
			body:     []byte(`<image href="/a.png"></image>`),
			header:   map[string]string{"Content-Type": "image/svg+xml"},
			wantBody: `<image href="/p/a.png"></image>`,
		},
		{
			name:     "plain text is rewritten",
			path:     "/t",
			body:     []byte(`"/a"`),
			header:   map[string]string{"Content-Type": "text/plain"},
			wantBody: `"/p/a"`,
		},
		{
			name:     "content type inferred from the request path",
			path:     "/s.css?v=1",
			body:     []byte(`a{b:url(/i.png)}`),
			header:   nil,
			wantBody: `a{b:url(/p/i.png)}`,
		},
		{name: "javascript content type untouched", path: "/a", body: []byte(`"/a"`), header: map[string]string{"Content-Type": "application/javascript"}, wantBody: `"/a"`},
		{name: "text/javascript untouched", path: "/a", body: []byte(`"/a"`), header: map[string]string{"Content-Type": "text/javascript; charset=utf-8"}, wantBody: `"/a"`},
		{name: "script path wins over a text type", path: "/app.js", body: []byte(`"/a"`), header: map[string]string{"Content-Type": "text/plain"}, wantBody: `"/a"`},
		{name: "module script path", path: "/app.mjs", body: []byte(`"/a"`), header: nil, wantBody: `"/a"`},
		{name: "event stream untouched", path: "/e", body: []byte(`data: "/a"`), header: map[string]string{"Content-Type": "text/event-stream"}, wantBody: `data: "/a"`},
		{name: "binary untouched", path: "/i.png", body: []byte(`"/a"`), header: map[string]string{"Content-Type": "image/png"}, wantBody: `"/a"`},
		{name: "unknown type without path hint untouched", path: "/blob", body: []byte(`"/a"`), header: nil, wantBody: `"/a"`},
		{name: "brotli untouched", path: "/page", body: []byte("br-bytes"), header: map[string]string{"Content-Type": "text/html", "Content-Encoding": "br"}, wantBody: "br-bytes", wantHeader: map[string]string{"Content-Encoding": "br"}},
		{
			name:     "gzip that changes is served decoded",
			path:     "/page",
			body:     gzChanged,
			header:   map[string]string{"Content-Type": "text/html", "Content-Encoding": "gzip", "ETag": `"v1"`},
			wantBody: `<a href="/p/x">`,
			wantHeader: map[string]string{
				"Content-Encoding": "", "ETag": "", "Content-Length": "15",
			},
		},
		{
			name:     "gzip that does not change is served as received",
			path:     "/page",
			body:     gzUnchanged,
			header:   map[string]string{"Content-Type": "text/html", "Content-Encoding": "gzip", "ETag": `"v1"`},
			wantBody: string(gzUnchanged),
			wantHeader: map[string]string{
				"Content-Encoding": "gzip", "ETag": `"v1"`, "Content-Length": strconv.Itoa(len(gzUnchanged)),
			},
		},
		{
			name:       "invalid gzip is passed through",
			path:       "/page",
			body:       []byte("not gzip"),
			header:     map[string]string{"Content-Type": "text/html", "Content-Encoding": "gzip"},
			wantBody:   "not gzip",
			wantHeader: map[string]string{"Content-Encoding": "gzip", "Content-Length": "8"},
		},
		{
			name:     "identity content encoding",
			path:     "/page",
			body:     []byte(`<a href="/x">`),
			header:   map[string]string{"Content-Type": "text/html", "Content-Encoding": "Identity"},
			wantBody: `<a href="/p/x">`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := bodyResponse(tc.path, tc.body, tc.header)
			if err := rewriteTextBody(resp, prefix); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wantBody {
				t.Fatalf("body:\n got %q\nwant %q", got, tc.wantBody)
			}
			for k, want := range tc.wantHeader {
				if have := resp.Header.Get(k); have != want {
					t.Errorf("header %s = %q, want %q", k, have, want)
				}
			}
		})
	}
}

func TestRewriteTextBodySizeCap(t *testing.T) {
	const maxBody = 8 << 20
	header := map[string]string{"Content-Type": "text/html"}

	t.Run("declared length above the cap is not read", func(t *testing.T) {
		resp := bodyResponse("/page", []byte(`<a href="/x">`), header)
		resp.ContentLength = maxBody + 1
		if err := rewriteTextBody(resp, "/p"); err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		if string(got) != `<a href="/x">` {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("undeclared length above the cap streams through intact", func(t *testing.T) {
		unit := []byte(`<a href="/x">`)
		body := bytes.Repeat(unit, maxBody/len(unit)+2)
		resp := bodyResponse("/page", body, header)
		resp.ContentLength = -1
		if err := rewriteTextBody(resp, "/p"); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("body changed or truncated: len=%d want=%d err=%v", len(got), len(body), err)
		}
		if resp.Header.Get("Content-Length") != "" {
			t.Fatalf("Content-Length must stay untouched, got %q", resp.Header.Get("Content-Length"))
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("gzip above the cap streams through intact", func(t *testing.T) {
		raw := bytes.Repeat([]byte{0xA5, 0x5A, 0xC3}, maxBody/3+10)
		resp := bodyResponse("/page", raw, map[string]string{"Content-Type": "text/html", "Content-Encoding": "gzip"})
		resp.ContentLength = -1
		if err := rewriteTextBody(resp, "/p"); err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(got, raw) || resp.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("oversized gzip body was modified")
		}
	})

	t.Run("gzip that inflates beyond the cap is served as received", func(t *testing.T) {
		packed := gzipBytes(t, strings.Repeat(`<a href="/x">`, maxBody/13+10))
		resp := bodyResponse("/page", packed, map[string]string{"Content-Type": "text/html", "Content-Encoding": "gzip"})
		if err := rewriteTextBody(resp, "/p"); err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(got, packed) || resp.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("zip-bomb sized body was rewritten")
		}
	})
}
