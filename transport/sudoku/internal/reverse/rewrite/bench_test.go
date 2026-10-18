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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	benchHTMLHit = `<link rel="stylesheet" href="/static/app.css"><img src="/img/a.png" srcset="/img/a.png 1x, /img/b.png 2x">` +
		`<a href="/docs/page">docs</a><script>fetch("/api/items")</script><div class="x">some text that is not a link</div>` + "\n"
	benchHTMLMiss = `<link rel="stylesheet" href="static/app.css"><img src="img/a.png" srcset="img/a.png 1x, img/b.png 2x">` +
		`<a href="docs/page">docs</a><script>fetch("api/items")</script><div class="x">some text that is not a link</div>` + "\n"
	benchBodyBytes = 256 << 10
)

func benchBody(unit string) []byte {
	return bytes.Repeat([]byte(unit), benchBodyBytes/len(unit))
}

func BenchmarkRewriteTextPayload(b *testing.B) {
	for _, tc := range []struct{ name, unit string }{{"hit", benchHTMLHit}, {"miss", benchHTMLMiss}} {
		in := benchBody(tc.unit)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(in)))
			for i := 0; i < b.N; i++ {
				rewriteTextPayload("text/html", in, "/p")
			}
		})
	}
}

func BenchmarkRewriteTextBody(b *testing.B) {
	for _, tc := range []struct {
		name, unit string
		gzip       bool
		knownLen   bool
	}{
		{"hit-length", benchHTMLHit, false, true},
		{"hit-chunked", benchHTMLHit, false, false},
		{"miss-length", benchHTMLMiss, false, true},
		{"hit-gzip", benchHTMLHit, true, true},
	} {
		raw := benchBody(tc.unit)
		wire := raw
		header := map[string]string{"Content-Type": "text/html"}
		if tc.gzip {
			wire = gzipBytes(b, string(raw))
			header["Content-Encoding"] = "gzip"
		}
		req := httptest.NewRequest(http.MethodGet, "http://front/index.html", nil)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for i := 0; i < b.N; i++ {
				resp := &http.Response{
					StatusCode:    http.StatusOK,
					Header:        http.Header{},
					Body:          io.NopCloser(bytes.NewReader(wire)),
					ContentLength: int64(len(wire)),
					Request:       req,
				}
				for k, v := range header {
					resp.Header.Set(k, v)
				}
				if !tc.knownLen {
					resp.ContentLength = -1
				}
				if err := rewriteTextBody(resp, "/p"); err != nil {
					b.Fatal(err)
				}
				if n, _ := io.Copy(io.Discard, resp.Body); n == 0 {
					b.Fatal("empty body")
				}
			}
		})
	}
}
