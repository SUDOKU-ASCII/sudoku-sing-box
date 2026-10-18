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
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/reverse/rewrite"
	"github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
)

func newRouteProxy(prefix, target string, stripPrefix bool, hostHeader string, mux *tunnel.MuxClient) *httputil.ReverseProxy {
	targetURL := &url.URL{Scheme: "http", Host: target}
	defaultHostHeader := defaultUpstreamHostHeader(target)
	// A subpath mount hides the prefix from upstream, so responses must get it back.
	rewritesResponses := stripPrefix && prefix != "/"
	// Director rather than Rewrite on purpose: Rewrite would drop the client's Forwarded/X-Forwarded-*
	// headers and sanitise the query string, which changes what existing upstream apps receive.
	rp := httputil.NewSingleHostReverseProxy(targetURL)

	// Each reverse request uses a fresh mux stream; do not let net/http attempt to keep idle conns.
	rp.Transport = &http.Transport{
		Proxy:              nil,
		DisableCompression: true,
		DisableKeepAlives:  true,
		ForceAttemptHTTP2:  false,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			return mux.Dial(target)
		},
	}

	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)

		if stripPrefix {
			req.URL.Path = stripPathPrefix(req.URL.Path, prefix)
			req.URL.RawPath = ""
		}
		if hostHeader != "" {
			req.Host = hostHeader
		} else if defaultHostHeader != "" {
			req.Host = defaultHostHeader
		}
		if prefix != "/" {
			req.Header.Set("X-Forwarded-Prefix", prefix)
		}
		if rewritesResponses {
			// Many web apps gzip/br their HTML/JS/CSS when the client sends Accept-Encoding.
			// Subpath support requires response rewriting, so force identity encoding upstream.
			req.Header.Del("Accept-Encoding")
		}
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		if rewritesResponses {
			return rewrite.Response(resp, prefix)
		}
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		// Avoid leaking internal details; the server logs should carry the rest.
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}

	// Reasonable default for streaming responses (SSE, long polls, etc.).
	rp.FlushInterval = 50 * time.Millisecond

	return rp
}

func stripPathPrefix(reqPath, prefix string) string {
	switch {
	case reqPath == prefix:
		return "/"
	case prefix != "/" && strings.HasPrefix(reqPath, prefix+"/"):
		return reqPath[len(prefix):]
	case reqPath == "":
		return "/"
	}
	return reqPath
}

func defaultUpstreamHostHeader(target string) string {
	target = strings.TrimSpace(target)
	host, _, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return ""
	}

	normalizedHost := host
	if i := strings.IndexByte(normalizedHost, '%'); i >= 0 {
		normalizedHost = normalizedHost[:i]
	}
	if ip := net.ParseIP(normalizedHost); ip != nil {
		return ""
	}

	return target
}
