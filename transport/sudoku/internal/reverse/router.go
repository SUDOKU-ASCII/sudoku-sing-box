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
	"net/http"
	"net/url"
	"strings"

	"github.com/sagernet/sing-box/transport/sudoku/internal/reverse/rewrite"
)

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	m.mu.RLock()
	entry := m.matchLocked(path)
	if entry == nil {
		entry = m.matchByRefererLocked(r)
	}
	if entry == nil {
		entry = m.matchByCookieLocked(r)
	}
	m.mu.RUnlock()

	if entry == nil {
		http.NotFound(w, r)
		return
	}

	// If the request path doesn't contain a reverse prefix but a Referer does (common for
	// root-absolute assets/API calls like "/static/app.js" or "/api/foo"), rewrite it into the
	// correct subpath so routing works without mutating response bodies.
	if entry.prefix != "/" && !pathPrefixMatch(path, entry.prefix) {
		r.URL.Path = entry.prefix + path
		r.URL.RawPath = ""
		path = r.URL.Path
	}

	// /<prefix> (no trailing slash) is reserved for Sudoku's TCP-over-WS tunnel when the client
	// explicitly negotiates the "sudoku-tcp-v1" subprotocol. Any other WS upgrade should fall
	// through to the normal reverse proxy (and must not be redirected).
	if path == entry.prefix {
		if isWebSocketUpgrade(r) {
			if websocketClientOffersSubprotocol(r, sudokuTCPSubprotocol) {
				serveSudokuTCPTunnel(w, r, entry.mux, entry.target)
				return
			}
		} else if entry.prefix != "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			// Many apps use relative asset URLs ("static/app.js"); without the trailing slash browsers
			// resolve them against "/", so the route root must look like a directory.
			u := *r.URL
			u.Path = path + "/"
			http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
			return
		}
	}

	entry.proxy.ServeHTTP(w, r)
}

func (m *Manager) matchLocked(reqPath string) *routeEntry {
	var (
		best     *routeEntry
		bestSize int
	)
	for prefix, entry := range m.routes {
		if !pathPrefixMatch(reqPath, prefix) {
			continue
		}
		if len(prefix) > bestSize {
			best = entry
			bestSize = len(prefix)
		}
	}
	return best
}

func (m *Manager) matchByRefererLocked(r *http.Request) *routeEntry {
	ref := strings.TrimSpace(r.Header.Get("Referer"))
	if ref == "" {
		return nil
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil
	}
	refPath := u.EscapedPath()
	if refPath == "" {
		refPath = u.Path
	}
	if refPath == "" {
		refPath = "/"
	}
	return m.matchLocked(refPath)
}

func (m *Manager) matchByCookieLocked(r *http.Request) *routeEntry {
	c, err := r.Cookie(rewrite.PrefixCookieName)
	if err != nil {
		return nil
	}
	prefix, err := url.PathUnescape(strings.TrimSpace(c.Value))
	if err != nil {
		return nil
	}
	prefix = rewrite.NormalizePrefix(prefix)
	if prefix == "" {
		return nil
	}
	return m.matchLocked(prefix)
}

func pathPrefixMatch(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	if prefix == "/" {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == '/'
}
