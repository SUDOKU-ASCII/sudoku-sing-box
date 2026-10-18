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
	"net"
	"slices"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
)

func newTestMux(t *testing.T) *tunnel.MuxClient {
	t.Helper()
	a, b := net.Pipe()
	mc, err := tunnel.NewMuxClient(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = mc.Close()
		_ = a.Close()
		_ = b.Close()
	})
	return mc
}

func boolPtr(v bool) *bool { return &v }

func TestParseRoutes(t *testing.T) {
	got := parseRoutes([]config.ReverseRoute{
		{Path: " /a ", Target: " t:1 ", HostHeader: " h.example ", StripPrefix: boolPtr(false)},
		{Path: "/b", Target: "t:2", StripPrefix: boolPtr(true)},
		{Path: "/c", Target: "t:3"},
		{},
	})
	want := []routeSpec{
		{prefix: "/a", target: "t:1", hostHeader: "h.example", stripPrefix: false},
		{prefix: "/b", target: "t:2", stripPrefix: true},
		{prefix: "/c", target: "t:3", stripPrefix: true},
		{stripPrefix: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parseRoutes = %+v, want %+v", got, want)
	}
}

func TestRegisterSession(t *testing.T) {
	t.Run("argument errors", func(t *testing.T) {
		mc := newTestMux(t)
		routes := []config.ReverseRoute{{Path: "/a", Target: "t:1"}}
		if err := NewManager().RegisterSession(nil, routes); err == nil || err.Error() != "nil mux client" {
			t.Errorf("nil mux: %v", err)
		}
		if err := NewManager().RegisterSession(mc, nil); err == nil || err.Error() != "no reverse routes" {
			t.Errorf("no routes: %v", err)
		}
	})

	t.Run("registers http and tcp routes", func(t *testing.T) {
		m := NewManager()
		mc := newTestMux(t)
		err := m.RegisterSession(mc, []config.ReverseRoute{
			{Path: " /a ", Target: " t:1 ", HostHeader: " h.example ", StripPrefix: boolPtr(false)},
			{Path: "/b", Target: "t:2"},
			{Path: "/skipped"},
			{},
			{Target: "tcp:3"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(m.routes) != 2 {
			t.Fatalf("routes = %d, want 2 (empty-target path routes are dropped)", len(m.routes))
		}
		if a := m.routes["/a"]; a == nil || a.target != "t:1" || a.mux != mc || a.proxy == nil {
			t.Fatalf("route /a = %+v", a)
		}
		if m.tcp == nil || m.tcp.target != "tcp:3" || m.tcp.mux != mc {
			t.Fatalf("tcp = %+v", m.tcp)
		}
		if err := m.RegisterSession(mc, []config.ReverseRoute{{Path: "/z", Target: "t:9"}}); err == nil ||
			err.Error() != "reverse session already registered" {
			t.Fatalf("same mux twice: %v", err)
		}
	})

	t.Run("conflicts leave the manager untouched", func(t *testing.T) {
		tests := []struct {
			name   string
			routes []config.ReverseRoute
			want   string
		}{
			{"path taken", []config.ReverseRoute{{Path: "/fresh", Target: "t:1"}, {Path: "/a", Target: "t:2"}}, `reverse path already registered: "/a"`},
			{"path taken even without target", []config.ReverseRoute{{Path: "/a"}}, `reverse path already registered: "/a"`},
			{"tcp taken", []config.ReverseRoute{{Path: "/fresh", Target: "t:1"}, {Target: "tcp:2"}}, "reverse tcp route already registered"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				m := NewManager()
				if err := m.RegisterSession(newTestMux(t), []config.ReverseRoute{{Path: "/a", Target: "t:0"}, {Target: "tcp:0"}}); err != nil {
					t.Fatal(err)
				}
				tcp := m.tcp
				err := m.RegisterSession(newTestMux(t), tc.routes)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("err = %v, want %q", err, tc.want)
				}
				if len(m.routes) != 1 || len(m.sessions) != 1 || m.tcp != tcp {
					t.Fatalf("state changed: routes=%d sessions=%d tcp=%+v", len(m.routes), len(m.sessions), m.tcp)
				}
			})
		}

		m := NewManager()
		err := m.RegisterSession(newTestMux(t), []config.ReverseRoute{{Target: "tcp:1"}, {Target: "tcp:2"}})
		if err == nil || err.Error() != "reverse tcp route already set in this session" {
			t.Fatalf("two tcp routes in one session: %v", err)
		}
		if len(m.sessions) != 0 || m.tcp != nil {
			t.Fatal("failed registration must not leave state behind")
		}
	})

	t.Run("closing the mux unregisters its routes", func(t *testing.T) {
		m := NewManager()
		mc := newTestMux(t)
		if err := m.RegisterSession(mc, []config.ReverseRoute{{Path: "/a", Target: "t:1"}, {Target: "tcp:2"}}); err != nil {
			t.Fatal(err)
		}
		_ = mc.Close()
		deadline := time.Now().Add(3 * time.Second)
		for {
			m.mu.RLock()
			empty := len(m.routes) == 0 && len(m.sessions) == 0 && m.tcp == nil
			m.mu.RUnlock()
			if empty {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("session routes were not released after the mux closed")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := m.RegisterSession(newTestMux(t), []config.ReverseRoute{{Path: "/a", Target: "t:1"}}); err != nil {
			t.Fatalf("path should be reusable after unregister: %v", err)
		}
	})
}
