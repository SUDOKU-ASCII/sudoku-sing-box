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
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func rewriteTextBody(resp *http.Response, prefix string) error {
	if resp == nil || resp.Body == nil || resp.Header == nil || prefix == "" || prefix == "/" {
		return nil
	}

	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		return nil
	}

	const maxBody = 8 << 20
	if resp.ContentLength > maxBody {
		return nil
	}

	reqPath := ""
	if resp.Request != nil && resp.Request.URL != nil {
		reqPath = resp.Request.URL.Path
	}
	pathCT := inferContentTypeFromPath(reqPath)

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "" {
		ct = pathCT
	}
	if ct == "" || ct == "text/event-stream" || isJavaScriptContentType(ct) || pathCT == "application/javascript" {
		return nil
	}
	if !isRewritableContentType(ct) {
		return nil
	}

	setBody := func(b []byte) {
		resp.Body = io.NopCloser(bytes.NewReader(b))
		resp.ContentLength = int64(len(b))
		resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
		resp.Header.Del("Transfer-Encoding")
	}
	restoreStream := func(buf []byte) {
		resp.Body = multiReadCloser{
			Reader: io.MultiReader(bytes.NewReader(buf), resp.Body),
			Closer: resp.Body,
		}
	}

	readAll := func(r io.Reader) ([]byte, error) {
		b, err := io.ReadAll(io.LimitReader(r, maxBody+1))
		if err != nil {
			return nil, err
		}
		return b, nil
	}

	if encoding == "gzip" {
		compressed, err := readAll(resp.Body)
		if err != nil {
			return err
		}
		if len(compressed) > maxBody {
			restoreStream(compressed)
			return nil
		}
		_ = resp.Body.Close()

		gr, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			setBody(compressed)
			return nil
		}
		raw, err := readAll(gr)
		_ = gr.Close()
		if err != nil {
			return err
		}
		if len(raw) > maxBody {
			setBody(compressed)
			return nil
		}

		rewritten := rewriteTextPayload(ct, raw, prefix)
		if bytes.Equal(raw, rewritten) {
			setBody(compressed)
			return nil
		}

		setBody(rewritten)
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("ETag")
		return nil
	}

	raw, err := readAll(resp.Body)
	if err != nil {
		return err
	}
	if len(raw) > maxBody {
		restoreStream(raw)
		return nil
	}
	_ = resp.Body.Close()

	rewritten := rewriteTextPayload(ct, raw, prefix)
	if !bytes.Equal(raw, rewritten) {
		resp.Header.Del("ETag")
	}
	setBody(rewritten)
	return nil
}

func inferContentTypeFromPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	switch {
	case strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm"):
		return "text/html"
	case strings.HasSuffix(p, ".css"):
		return "text/css"
	case strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs"):
		return "application/javascript"
	case strings.HasSuffix(p, ".json"):
		return "application/json"
	case strings.HasSuffix(p, ".webmanifest"):
		return "application/manifest+json"
	case strings.HasSuffix(p, ".svg"):
		return "image/svg+xml"
	default:
		return ""
	}
}

func rewriteTextPayload(contentType string, in []byte, prefix string) []byte {
	if in == nil {
		return nil
	}
	if contentType == "text/html" || contentType == "application/xhtml+xml" {
		out := rewriteHTMLRootAbsolutePaths(in, prefix)
		return rewriteHTMLSrcset(out, prefix)
	}
	return rewriteRootAbsolutePaths(in, prefix)
}

func isJavaScriptContentType(contentType string) bool {
	switch contentType {
	case "application/javascript", "application/x-javascript", "text/javascript", "text/ecmascript", "application/ecmascript":
		return true
	default:
		return false
	}
}

func isRewritableContentType(ct string) bool {
	switch {
	case strings.HasPrefix(ct, "text/"):
		return !isJavaScriptContentType(ct)
	case ct == "application/json", ct == "application/manifest+json", ct == "application/xhtml+xml", ct == "image/svg+xml":
		return true
	default:
		return false
	}
}

type multiReadCloser struct {
	io.Reader
	io.Closer
}
