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
	"net/http/httptest"
	"testing"
)

func TestWebSocketHeaderHelpers(t *testing.T) {
	upgrade := func(upgradeValue string, connection ...string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if upgradeValue != "" {
			r.Header.Set("Upgrade", upgradeValue)
		}
		for _, c := range connection {
			r.Header.Add("Connection", c)
		}
		return r
	}
	isUpgrade := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"plain upgrade", upgrade("websocket", "Upgrade"), true},
		{"case-insensitive values", upgrade("WebSocket", "uPgRaDe"), true},
		{"token in a list", upgrade("websocket", "keep-alive, Upgrade"), true},
		{"token in a second header line", upgrade("websocket", "keep-alive", "Upgrade"), true},
		{"missing connection", upgrade("websocket"), false},
		{"connection without upgrade token", upgrade("websocket", "keep-alive"), false},
		{"other upgrade protocol", upgrade("h2c", "Upgrade"), false},
		{"missing upgrade", upgrade("", "Upgrade"), false},
	}
	for _, tc := range isUpgrade {
		if got := isWebSocketUpgrade(tc.r); got != tc.want {
			t.Errorf("isWebSocketUpgrade(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	offers := []struct {
		name   string
		values []string
		want   bool
	}{
		{"single", []string{"sudoku-tcp-v1"}, true},
		{"in a list", []string{"chat, sudoku-tcp-v1"}, true},
		{"second header line", []string{"chat", "sudoku-tcp-v1"}, true},
		{"surrounding spaces", []string{"  sudoku-tcp-v1  "}, true},
		{"subprotocol names are case-sensitive", []string{"Sudoku-TCP-V1"}, false},
		{"different protocol", []string{"sudoku-tcp-v2"}, false},
		{"absent", nil, false},
	}
	for _, tc := range offers {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, v := range tc.values {
			r.Header.Add("Sec-WebSocket-Protocol", v)
		}
		if got := websocketClientOffersSubprotocol(r, sudokuTCPSubprotocol); got != tc.want {
			t.Errorf("websocketClientOffersSubprotocol(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
