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
	"iter"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
)

const sudokuTCPSubprotocol = "sudoku-tcp-v1"

func serveSudokuTCPTunnel(w http.ResponseWriter, r *http.Request, mux *tunnel.MuxClient, target string) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{sudokuTCPSubprotocol},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		// Accept already wrote the response.
		return
	}
	if c.Subprotocol() != sudokuTCPSubprotocol {
		_ = c.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return
	}

	up, err := mux.Dial(target)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "dial failed")
		return
	}

	wsConn := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
	tunnel.PipeConn(wsConn, up)
}

func websocketClientOffersSubprotocol(r *http.Request, want string) bool {
	for token := range headerTokens(r.Header, "Sec-WebSocket-Protocol") {
		if token == want {
			return true
		}
	}
	return false
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for token := range headerTokens(r.Header, "Connection") {
		if strings.EqualFold(token, "upgrade") {
			return true
		}
	}
	return false
}

// headerTokens yields the trimmed comma-separated tokens of every line of the key header.
func headerTokens(h http.Header, key string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, line := range h.Values(key) {
			for part := range strings.SplitSeq(line, ",") {
				if !yield(strings.TrimSpace(part)) {
					return
				}
			}
		}
	}
}
