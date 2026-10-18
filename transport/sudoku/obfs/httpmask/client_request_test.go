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
package httpmask

import (
	"net/http"
	"net/textproto"
	"slices"
	"testing"
)

func TestApplyTunnelHeaders_CanonicalKeysAndValues(t *testing.T) {
	for _, mode := range []TunnelMode{TunnelModeStream, TunnelModePoll, TunnelModeAuto, TunnelModeWS, TunnelMode("custom")} {
		h := http.Header{}
		applyTunnelHeaders(h, "example.com", mode)

		for key := range h {
			if key != textproto.CanonicalMIMEHeaderKey(key) {
				t.Errorf("%s: key %q is not canonical, so Header.Get would miss it", mode, key)
			}
		}
		if got := h.Get("X-Sudoku-Tunnel"); got != string(mode) {
			t.Errorf("%s: X-Sudoku-Tunnel = %q", mode, got)
		}
		if got := h.Get("Host"); got != "example.com" {
			t.Errorf("%s: Host = %q", mode, got)
		}
		if got, want := h.Get("Connection"), map[bool]string{true: "", false: "keep-alive"}[mode == TunnelModeWS]; got != want {
			t.Errorf("%s: Connection = %q, want %q", mode, got, want)
		}
		for _, key := range []string{"Cache-Control", "Pragma"} {
			if got := h.Get(key); got != "no-cache" {
				t.Errorf("%s: %s = %q", mode, key, got)
			}
		}
		if !slices.Contains(userAgents[:], h.Get("User-Agent")) ||
			!slices.Contains(accepts[:], h.Get("Accept")) ||
			!slices.Contains(acceptLanguages[:], h.Get("Accept-Language")) {
			t.Errorf("%s: profile headers not drawn from the tables: %v", mode, h)
		}
	}
}
