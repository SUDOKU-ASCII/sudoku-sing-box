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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	goldenWSKey    = "dGhlIHNhbXBsZSBub25jZQ=="
	goldenWSAccept = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	goldenWSSecret = "golden-key"
)

func goldenWSRequest(method, target string, headers ...string) string {
	var b strings.Builder
	b.WriteString(method + " " + target + " HTTP/1.1\r\nHost: " + goldenHost + "\r\n")
	b.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + goldenWSKey + "\r\nSec-WebSocket-Version: 13\r\n")
	for _, h := range headers {
		b.WriteString(h + "\r\n")
	}
	b.WriteString("\r\n")
	return b.String()
}

func goldenWSToken(t *testing.T, path string, at time.Time) string {
	t.Helper()
	return newTunnelAuth(goldenWSSecret, 0).token(TunnelModeWS, http.MethodGet, path, at)
}

func TestGoldenWSAuthToken(t *testing.T) {
	at := time.Unix(1700000000, 0)
	const want = "AAAAAGVT8QAJHu_Fuwf5lL-TIBqcSaOL"
	got := newTunnelAuth(goldenWSSecret, 0).token(TunnelModeWS, "GET", "/ws", at)

	// Independent re-derivation of the documented construction.
	keySum := sha256.Sum256([]byte("sudoku-httpmask-auth-v1:" + goldenWSSecret))
	mac := hmac.New(sha256.New, keySum[:])
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(at.Unix()))
	mac.Write([]byte("ws\x00GET\x00/ws\x00"))
	mac.Write(ts[:])
	raw := append(append([]byte{}, ts[:]...), mac.Sum(nil)[:16]...)
	if spec := base64.RawURLEncoding.EncodeToString(raw); got != spec {
		t.Fatalf("token %q does not match documented construction %q", got, spec)
	}
	if got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
}

func TestGoldenWSAuthVerify(t *testing.T) {
	auth := newTunnelAuth(goldenWSSecret, 0)
	now := time.Unix(1700000000, 0)
	tok := auth.token(TunnelModeWS, "GET", "/ws", now)

	cases := []struct {
		name string
		val  string
		mode TunnelMode
		meth string
		path string
		at   time.Time
		want bool
	}{
		{"bearer", "Bearer " + tok, TunnelModeWS, "GET", "/ws", now, true},
		{"bearer is case-insensitive", "bEaReR " + tok, TunnelModeWS, "GET", "/ws", now, true},
		{"raw token", tok, TunnelModeWS, "GET", "/ws", now, true},
		{"method is case-insensitive", tok, TunnelModeWS, "get", "/ws", now, true},
		{"empty method means GET", tok, TunnelModeWS, "", "/ws", now, true},
		{"skew edge accepted", tok, TunnelModeWS, "GET", "/ws", now.Add(60 * time.Second), true},
		{"skew exceeded", tok, TunnelModeWS, "GET", "/ws", now.Add(61 * time.Second), false},
		{"skew exceeded backwards", tok, TunnelModeWS, "GET", "/ws", now.Add(-61 * time.Second), false},
		{"other path", tok, TunnelModeWS, "GET", "/ws2", now, false},
		{"path root is not signed away", tok, TunnelModeWS, "GET", "/r/ws", now, false},
		{"other method", tok, TunnelModeWS, "POST", "/ws", now, false},
		{"other mode", tok, TunnelModeStream, "GET", "/ws", now, false},
		{"empty", "", TunnelModeWS, "GET", "/ws", now, false},
		{"bearer without token", "Bearer ", TunnelModeWS, "GET", "/ws", now, false},
		{"garbage", "***", TunnelModeWS, "GET", "/ws", now, false},
		{"truncated", tok[:len(tok)-2], TunnelModeWS, "GET", "/ws", now, false},
	}
	for _, tc := range cases {
		if got := auth.verifyValue(tc.val, tc.mode, tc.meth, tc.path, tc.at); got != tc.want {
			t.Errorf("%s: verifyValue = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !(*tunnelAuth)(nil).verifyValue("", TunnelModeWS, "GET", "/ws", now) {
		t.Error("nil auth must accept everything")
	}
	if newTunnelAuth("   ", 0) != nil {
		t.Error("blank key must disable auth")
	}
	if newTunnelAuth("other", 0).verifyValue(tok, TunnelModeWS, "GET", "/ws", now) {
		t.Error("token verified under a different key")
	}
}

func TestGoldenWSApplyAuthHeaderAndQuery(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://golden.test/r/ws?ed=abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := newTunnelAuth(goldenWSSecret, 0)
	before := time.Now()
	applyTunnelAuth(req, auth, TunnelModeWS, http.MethodGet, "/ws")

	header := req.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		t.Fatalf("Authorization = %q", header)
	}
	if q := req.URL.Query(); q.Get("auth") != token || q.Get("ed") != "abc" {
		t.Fatalf("query = %q", req.URL.RawQuery)
	}
	if req.URL.RawQuery != "auth="+token+"&ed=abc" {
		t.Fatalf("query order = %q", req.URL.RawQuery)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 24 {
		t.Fatalf("token %q decodes to %d bytes err=%v", token, len(raw), err)
	}
	if ts := int64(binary.BigEndian.Uint64(raw[:8])); ts < before.Unix() || ts > time.Now().Unix() {
		t.Fatalf("token timestamp %d outside [%d, %d]", ts, before.Unix(), time.Now().Unix())
	}
	if !auth.verifyValue(header, TunnelModeWS, http.MethodGet, "/ws", time.Now()) {
		t.Fatal("applied token does not verify")
	}

	untouched, _ := http.NewRequest(http.MethodGet, "http://golden.test/ws", nil)
	applyTunnelAuth(untouched, nil, TunnelModeWS, http.MethodGet, "/ws")
	if untouched.Header.Get("Authorization") != "" || untouched.URL.RawQuery != "" {
		t.Fatal("nil auth must not touch the request")
	}
}

func TestGoldenWSServerRejections(t *testing.T) {
	fresh := goldenWSToken(t, "/ws", time.Now())
	rootedSigned := goldenWSToken(t, "/r/ws", time.Now())
	stale := goldenWSToken(t, "/ws", time.Now().Add(-5*time.Minute))
	bearer := func(tok string) string { return "Authorization: Bearer " + tok }
	prepareFails := &TunnelServerEarlyHandshake{Prepare: func([]byte) (*PreparedServerEarlyHandshake, error) {
		return nil, errors.New("denied")
	}}
	early := base64.RawURLEncoding.EncodeToString([]byte("hi"))

	cases := []struct {
		name  string
		root  string
		early *TunnelServerEarlyHandshake
		req   string
		want  string
	}{
		{"missing auth", "", nil, goldenWSRequest("GET", "/ws"), goldenReply404},
		{"stale token", "", nil, goldenWSRequest("GET", "/ws", bearer(stale)), goldenReply404},
		{"garbage token", "", nil, goldenWSRequest("GET", "/ws", bearer("nope")), goldenReply404},
		{"header wins over valid query", "", nil, goldenWSRequest("GET", "/ws?auth="+fresh, bearer("nope")), goldenReply404},
		{"wrong path", "", nil, goldenWSRequest("GET", "/ws2", bearer(fresh)), goldenReply404},
		{"wrong path prefix", "", nil, goldenWSRequest("GET", "/x/ws", bearer(fresh)), goldenReply404},
		{"missing root", "/r", nil, goldenWSRequest("GET", "/ws", bearer(fresh)), goldenReply404},
		{"root alone", "/r", nil, goldenWSRequest("GET", "/r", bearer(fresh)), goldenReply404},
		{"root without slash", "/r", nil, goldenWSRequest("GET", "/rws", bearer(fresh)), goldenReply404},
		{"token signed with the root", "/r", nil, goldenWSRequest("GET", "/r/ws", bearer(rootedSigned)), goldenReply404},
		{"method is checked before auth", "", nil, goldenWSRequest("POST", "/ws", bearer("nope")), goldenReply400},
		{"auth is checked before early data", "", nil, goldenWSRequest("GET", "/ws?ed=!!!", bearer("nope")), goldenReply404},
		{"undecodable early data", "", nil, goldenWSRequest("GET", "/ws?ed=!!!", bearer(fresh)), goldenReply400},
		{"early handshake refused", "", prepareFails, goldenWSRequest("GET", "/ws?ed="+early, bearer(fresh)), goldenReply404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := TunnelServerOptions{Mode: "ws", PathRoot: tc.root, AuthKey: goldenWSSecret, EarlyHandshake: tc.early}
			ex := runGoldenExchange(t, NewTunnelServer(opts), tc.req, false)
			if ex.result != HandleDone || ex.reply != tc.want {
				t.Fatalf("result=%v reply=%q, want %q", ex.result, ex.reply, tc.want)
			}

			opts.PassThroughOnReject = true
			requireGoldenReplay(t, runGoldenExchange(t, NewTunnelServer(opts), tc.req, false), tc.req, true)
		})
	}
}

func TestGoldenWSServerHandshake(t *testing.T) {
	early := base64.RawURLEncoding.EncodeToString([]byte("hi"))
	respondEarly := &TunnelServerEarlyHandshake{Prepare: func(payload []byte) (*PreparedServerEarlyHandshake, error) {
		return &PreparedServerEarlyHandshake{ResponsePayload: []byte("resp")}, nil
	}}
	signed := func(path string) string { return goldenWSToken(t, path, time.Now()) }

	cases := []struct {
		name      string
		root      string
		early     *TunnelServerEarlyHandshake
		authKey   string
		target    func() string
		headers   func() []string
		wantExtra []string
	}{
		{
			name:    "bearer header",
			authKey: goldenWSSecret,
			target:  func() string { return "/ws" },
			headers: func() []string { return []string{"Authorization: Bearer " + signed("/ws")} },
		},
		{
			name:    "auth query",
			authKey: goldenWSSecret,
			target:  func() string { return "/ws?auth=" + signed("/ws") },
			headers: func() []string { return nil },
		},
		{
			name:    "path root signs the stripped path",
			root:    "/r",
			authKey: goldenWSSecret,
			target:  func() string { return "/r/ws" },
			headers: func() []string { return []string{"Authorization: Bearer " + signed("/ws")} },
		},
		{
			name:    "no auth key configured",
			target:  func() string { return "/ws" },
			headers: func() []string { return nil },
		},
		{
			name:      "early data round trip",
			early:     respondEarly,
			target:    func() string { return "/ws?ed=" + early },
			headers:   func() []string { return nil },
			wantExtra: []string{"X-Sudoku-Early: cmVzcA"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: "ws", PathRoot: tc.root, AuthKey: tc.authKey, EarlyHandshake: tc.early})
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			_ = client.SetDeadline(time.Now().Add(10 * time.Second))

			go func() { _, _ = client.Write([]byte(goldenWSRequest("GET", tc.target(), tc.headers()...))) }()
			type handled struct {
				result HandleResult
				conn   net.Conn
				err    error
			}
			done := make(chan handled, 1)
			go func() {
				result, conn, err := srv.HandleConn(server)
				done <- handled{result, conn, err}
			}()

			br := bufio.NewReader(client)
			var lines []string
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					t.Fatalf("read handshake: %v (got %q)", err, lines)
				}
				line = strings.TrimRight(line, "\r\n")
				if line == "" {
					break
				}
				lines = append(lines, line)
			}
			h := <-done
			if h.result != HandleStartTunnel || h.err != nil {
				t.Fatalf("result=%v err=%v", h.result, h.err)
			}
			// Drop the client first so the websocket close handshake does not wait out its timeout.
			defer func() { _ = client.Close(); _ = h.conn.Close() }()

			// hijackRW emits headers in map order, so compare as a set.
			if lines[0] != "HTTP/1.1 101 Switching Protocols" {
				t.Fatalf("status line = %q", lines[0])
			}
			got := append([]string(nil), lines[1:]...)
			want := append([]string{"Connection: Upgrade", "Sec-Websocket-Accept: " + goldenWSAccept, "Upgrade: websocket"}, tc.wantExtra...)
			sort.Strings(got)
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("handshake headers\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestGoldenWSQueryAuthIsUrlEscapedSafe(t *testing.T) {
	tok := goldenWSToken(t, "/ws", time.Now())
	if q := url.QueryEscape(tok); q != tok {
		t.Fatalf("token %q changes under query escaping (%q)", tok, q)
	}
}
