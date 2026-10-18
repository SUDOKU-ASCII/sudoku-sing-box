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
	"bufio"
	"io"
	"strings"
	"testing"
)

func parseTestRequest(t *testing.T, raw string) *tunnelRequest {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(raw))
	req, _, err := readHTTPRequest(nil, br, []byte(raw[:4]))
	if err != nil {
		t.Fatalf("readHTTPRequest: %v", err)
	}
	return req
}

func TestReadHTTPRequest_Fields(t *testing.T) {
	req := parseTestRequest(t, "POST /a/b?x=1 HTTP/1.1\r\n"+
		"Host: example.com\r\n"+
		"x-SUDOKU-tunnel:  poll \r\n"+
		"X-Sudoku-Tunnel: stream\r\n"+ // first value wins
		"Content-Length: 5\r\n"+
		"No-Colon-Line\r\n"+
		"Kudoku: ignored\r\n"+
		"Transfer-Encoding:\r\n"+
		"Transfer-Encoding: chunked\r\n"+ // an empty first value still wins
		"\r\nhello")
	if req.method != "POST" || req.target != "/a/b?x=1" || req.proto != "HTTP/1.1" {
		t.Fatalf("request line = %q %q %q", req.method, req.target, req.proto)
	}
	if req.tunnel != "poll" || req.contentLength != "5" || req.transferEncoding != "" {
		t.Fatalf("fields = tunnel %q, content-length %q, transfer-encoding %q", req.tunnel, req.contentLength, req.transferEncoding)
	}
	body, length, err := req.body()
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	got, _ := io.ReadAll(body)
	if string(got) != "hello" || length != 5 {
		t.Fatalf("body = %q (declared %d)", got, length)
	}
}

func TestReadHTTPRequest_ReplayPrefixIsTheRequestAsReceived(t *testing.T) {
	const raw = "POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 3\r\n\r\nabc"
	req := parseTestRequest(t, raw)
	if got := string(req.replayPrefix()); got != raw {
		t.Fatalf("replay prefix = %q, want %q", got, raw)
	}
}

func TestReadHTTPRequest_InvalidReplaysEverythingRead(t *testing.T) {
	for name, raw := range map[string]string{
		"request line without proto": "GET /only-two\r\n\r\n",
		"header too large":           "GET / HTTP/1.1\r\n" + strings.Repeat("A: "+strings.Repeat("b", 3000)+"\r\n", 12),
		"truncated header":           "GET / HTTP/1.1\r\nHost: h\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			br := bufio.NewReader(strings.NewReader(raw))
			req, replay, err := readHTTPRequest(nil, br, []byte(raw[:4]))
			if err == nil || req != nil {
				t.Fatalf("expected a parse error, got req=%v err=%v", req, err)
			}
			if !strings.HasPrefix(raw, string(replay)) || len(replay) < 4 {
				t.Fatalf("replay %q is not a prefix of the input", replay)
			}
		})
	}
}

func TestTunnelRequest_IsWebSocketUpgrade(t *testing.T) {
	for _, tc := range []struct {
		upgrade, connection string
		want                bool
	}{
		{"websocket", "Upgrade", true},
		{"WebSocket", "keep-alive, upgrade", true},
		{"websocket", "keep-alive", false},
		{"h2c", "Upgrade", false},
		{"", "", false},
	} {
		req := &tunnelRequest{upgrade: tc.upgrade, connection: tc.connection}
		if got := req.isWebSocketUpgrade(); got != tc.want {
			t.Errorf("upgrade=%q connection=%q: got %v, want %v", tc.upgrade, tc.connection, got, tc.want)
		}
	}
}
