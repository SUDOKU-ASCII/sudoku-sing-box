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
	"net/url"
	"strings"
)

// PrefixCookieName remembers which route a browser is using so root-absolute requests can be routed back to it.
const PrefixCookieName = "sudoku_rev_prefix"

// underPrefix reports whether path is prefix itself or lies below it.
func underPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func rewriteLocation(resp *http.Response, prefix string) {
	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if loc == "" {
		return
	}

	// Root-absolute redirect.
	if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		u, err := url.Parse(loc)
		if err != nil || u.Path == "" || !strings.HasPrefix(u.Path, "/") {
			return
		}
		if underPrefix(u.Path, prefix) {
			return
		}
		u.Path = prefix + u.Path
		u.RawPath = ""
		resp.Header.Set("Location", u.String())
		return
	}

	// Absolute redirect to the same host.
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		u, err := url.Parse(loc)
		if err != nil || u.Host == "" || u.Path == "" || !strings.HasPrefix(u.Path, "/") {
			return
		}
		if underPrefix(u.Path, prefix) {
			return
		}
		if !sameHTTPHost(resp.Request.Host, u.Host, u.Scheme) {
			return
		}
		u.Path = prefix + u.Path
		u.RawPath = ""
		resp.Header.Set("Location", u.String())
		return
	}
}

func sameHTTPHost(reqHost, locHost, scheme string) bool {
	reqHost = strings.TrimSpace(reqHost)
	locHost = strings.TrimSpace(locHost)
	if reqHost == "" || locHost == "" {
		return false
	}

	reqURL := &url.URL{Host: reqHost}
	locURL := &url.URL{Host: locHost}

	if !strings.EqualFold(reqURL.Hostname(), locURL.Hostname()) {
		return false
	}

	reqPort := reqURL.Port()
	if reqPort == "" {
		// If the request Host has no port, accept same-host redirects regardless of default port.
		return true
	}

	locPort := locURL.Port()
	if locPort == "" {
		switch strings.ToLower(strings.TrimSpace(scheme)) {
		case "https":
			locPort = "443"
		case "http":
			locPort = "80"
		default:
			return false
		}
	}
	return reqPort == locPort
}

func rewriteSetCookiePath(resp *http.Response, prefix string) {
	values := resp.Header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}

	out := make([]string, 0, len(values))
	for _, v := range values {
		parts := strings.Split(v, ";")
		for i := range parts {
			part := strings.TrimSpace(parts[i])
			if len(part) < 5 {
				continue
			}
			// Case-insensitive "Path="
			if strings.EqualFold(part[:5], "Path=") {
				pathVal := strings.TrimSpace(part[5:])
				if strings.HasPrefix(pathVal, "/") && !strings.HasPrefix(pathVal, "//") {
					if underPrefix(pathVal, prefix) {
						continue
					}
					parts[i] = "Path=" + prefix + pathVal
				}
			}
		}
		out = append(out, strings.Join(parts, ";"))
	}

	resp.Header.Del("Set-Cookie")
	for _, v := range out {
		resp.Header.Add("Set-Cookie", v)
	}
}

func rewriteReversePrefixCookie(resp *http.Response, prefix string) {
	p := NormalizePrefix(prefix)
	if p == "" {
		return
	}
	c := (&http.Cookie{
		Name:     PrefixCookieName,
		Value:    url.PathEscape(p),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String()
	resp.Header.Add("Set-Cookie", c)
}
