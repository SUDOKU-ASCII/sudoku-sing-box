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
	"fmt"
	"net"
	"net/http/httputil"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
)

type Manager struct {
	mu sync.RWMutex

	routes   map[string]*routeEntry
	sessions map[*tunnel.MuxClient]*session
	tcp      *tcpEntry
}

type session struct {
	mux      *tunnel.MuxClient
	prefixes []string
	tcp      bool
}

type routeEntry struct {
	prefix string
	target string
	mux    *tunnel.MuxClient
	proxy  *httputil.ReverseProxy
}

type tcpEntry struct {
	target string
	mux    *tunnel.MuxClient
}

// routeSpec is a client's reverse route with whitespace trimmed and defaults applied.
type routeSpec struct {
	prefix      string
	target      string
	hostHeader  string
	stripPrefix bool
}

func NewManager() *Manager {
	return &Manager{
		routes:   make(map[string]*routeEntry),
		sessions: make(map[*tunnel.MuxClient]*session),
	}
}

func parseRoutes(routes []config.ReverseRoute) []routeSpec {
	specs := make([]routeSpec, len(routes))
	for i, r := range routes {
		specs[i] = routeSpec{
			prefix:      strings.TrimSpace(r.Path),
			target:      strings.TrimSpace(r.Target),
			hostHeader:  strings.TrimSpace(r.HostHeader),
			stripPrefix: r.StripPrefix == nil || *r.StripPrefix,
		}
	}
	return specs
}

// RegisterSession registers a reverse client session and its routes.
//
// On conflict (same path prefix already registered), it returns an error.
func (m *Manager) RegisterSession(mux *tunnel.MuxClient, routes []config.ReverseRoute) error {
	if mux == nil {
		return fmt.Errorf("nil mux client")
	}
	if len(routes) == 0 {
		return fmt.Errorf("no reverse routes")
	}

	if err := m.addSession(mux, parseRoutes(routes)); err != nil {
		return err
	}

	go func() {
		<-mux.Done()
		m.UnregisterSession(mux)
		_ = mux.Close()
	}()

	return nil
}

func (m *Manager) addSession(mux *tunnel.MuxClient, specs []routeSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[mux]; ok {
		return fmt.Errorf("reverse session already registered")
	}
	if err := m.checkConflictsLocked(specs); err != nil {
		return err
	}

	sess := &session{mux: mux}
	for _, s := range specs {
		switch {
		case s.target == "":
		case s.prefix == "":
			m.tcp = &tcpEntry{target: s.target, mux: mux}
			sess.tcp = true
		default:
			m.routes[s.prefix] = &routeEntry{
				prefix: s.prefix,
				target: s.target,
				mux:    mux,
				proxy:  newRouteProxy(s.prefix, s.target, s.stripPrefix, s.hostHeader, mux),
			}
			sess.prefixes = append(sess.prefixes, s.prefix)
		}
	}
	m.sessions[mux] = sess
	return nil
}

func (m *Manager) checkConflictsLocked(specs []routeSpec) error {
	seenTCP := false
	for _, s := range specs {
		if s.prefix != "" {
			if _, ok := m.routes[s.prefix]; ok {
				return fmt.Errorf("reverse path already registered: %q", s.prefix)
			}
			continue
		}
		// Path empty => raw TCP reverse on reverse.listen (no HTTP path prefix).
		if s.target == "" {
			continue
		}
		if seenTCP {
			return fmt.Errorf("reverse tcp route already set in this session")
		}
		seenTCP = true
		if m.tcp != nil {
			return fmt.Errorf("reverse tcp route already registered")
		}
	}
	return nil
}

func (m *Manager) UnregisterSession(mux *tunnel.MuxClient) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess := m.sessions[mux]
	if sess == nil {
		return
	}
	delete(m.sessions, mux)
	if sess.tcp {
		m.tcp = nil
	}
	for _, p := range sess.prefixes {
		delete(m.routes, p)
	}
}

// ServeTCP handles a raw TCP connection by forwarding it through the reverse session.
//
// It requires a reverse route with an empty path (Path=="") to be registered.
func (m *Manager) ServeTCP(conn net.Conn) {
	m.mu.RLock()
	ent := m.tcp
	m.mu.RUnlock()

	if ent == nil {
		_ = conn.Close()
		return
	}

	up, err := ent.mux.Dial(ent.target)
	if err != nil {
		_ = conn.Close()
		return
	}

	tunnel.PipeConn(conn, up)
}
