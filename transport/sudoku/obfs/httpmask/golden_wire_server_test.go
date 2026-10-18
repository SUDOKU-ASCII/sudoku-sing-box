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
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

// These tests pin the exact bytes the server puts on the wire. The expected
// literals were verified against the v0.5.1 release source; they must keep
// passing unchanged across refactors.

const goldenHost = "golden.test"

const (
	goldenReply200            = "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK"
	goldenReply400            = "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: 11\r\nConnection: close\r\n\r\nbad request"
	goldenReply400MissingTok  = "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nContent-Length: 13\r\nConnection: close\r\n\r\nmissing token"
	goldenReply403            = "HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\nContent-Length: 9\r\nConnection: close\r\n\r\nforbidden"
	goldenReply404            = "HTTP/1.1 404 Not Found\r\nContent-Type: text/plain\r\nContent-Length: 9\r\nConnection: close\r\n\r\nnot found"
	goldenReply409            = "HTTP/1.1 409 Conflict\r\nContent-Type: text/plain\r\nContent-Length: 12\r\nConnection: close\r\n\r\nsequence gap"
	goldenReply410            = "HTTP/1.1 410 Gone\r\nContent-Type: text/plain\r\nContent-Length: 4\r\nConnection: close\r\n\r\ngone"
	goldenReply413            = "HTTP/1.1 413 Request Entity Too Large\r\nContent-Type: text/plain\r\nContent-Length: 9\r\nConnection: close\r\n\r\ntoo large"
	goldenReply500            = "HTTP/1.1 500 Internal Server Error\r\nContent-Type: text/plain\r\nContent-Length: 14\r\nConnection: close\r\n\r\ninternal error"
	goldenAuthorizeHeadFormat = "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nCache-Control: no-store\r\nPragma: no-cache\r\nContent-Length: %d\r\nConnection: close\r\n\r\n"

	goldenPullHeader = "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nTransfer-Encoding: chunked\r\nTrailer: X-Sudoku-Stream-EOF\r\nCache-Control: no-store\r\nPragma: no-cache\r\nConnection: keep-alive\r\nX-Accel-Buffering: no\r\n\r\n"
	goldenOneHeader  = "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nTransfer-Encoding: chunked\r\nCache-Control: no-store\r\nPragma: no-cache\r\nConnection: keep-alive\r\nX-Accel-Buffering: no\r\n\r\n"
)

var goldenTokenPattern = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

func goldenRequest(method, target, tunnel, body string, headers ...string) string {
	var b strings.Builder
	b.WriteString(method + " " + target + " HTTP/1.1\r\nHost: " + goldenHost + "\r\n")
	if tunnel != "" {
		b.WriteString("X-Sudoku-Tunnel: " + tunnel + "\r\n")
	}
	hasLength := false
	for _, h := range headers {
		hasLength = hasLength || strings.HasPrefix(h, "Content-Length:")
		b.WriteString(h + "\r\n")
	}
	if method == "POST" && !hasLength {
		b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

type goldenExchange struct {
	result HandleResult
	conn   net.Conn
	err    error
	reply  string
}

// runGoldenExchange feeds request to srv.HandleConn over a pipe and returns the
// handler outcome plus every byte the server wrote before it closed the socket.
// With eof the client closes right after sending, simulating a truncated request.
func runGoldenExchange(t *testing.T, srv *TunnelServer, request string, eof bool) goldenExchange {
	t.Helper()

	client, server := net.Pipe()
	watchdog := time.AfterFunc(10*time.Second, func() { _ = client.Close(); _ = server.Close() })
	defer watchdog.Stop()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	go func() {
		_, _ = client.Write([]byte(request))
		if eof {
			_ = client.Close()
		}
	}()
	replyCh := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(client)
		replyCh <- string(b)
	}()

	result, conn, err := srv.HandleConn(server)
	if result == HandlePassThrough {
		_ = client.Close()
	}
	return goldenExchange{result: result, conn: conn, err: err, reply: <-replyCh}
}

func openGoldenSession(t *testing.T, srv *TunnelServer, tunnel, root string) (token string, app net.Conn) {
	t.Helper()
	ex := runGoldenExchange(t, srv, goldenRequest("GET", root+"/session", tunnel, ""), false)
	if ex.result != HandleStartTunnel || ex.err != nil {
		t.Fatalf("authorize: result=%v err=%v reply=%q", ex.result, ex.err, ex.reply)
	}
	m := goldenTokenPattern.FindStringSubmatch(ex.reply)
	if m == nil {
		t.Fatalf("authorize reply has no token: %q", ex.reply)
	}
	t.Cleanup(func() {
		srv.sessions.close(m[1])
		_ = ex.conn.Close()
	})
	return m[1], ex.conn
}

func goldenAppWrite(app net.Conn, data []byte, closeWrite bool) {
	go func() {
		if len(data) > 0 {
			_, _ = app.Write(data)
		}
		if closeWrite {
			_ = connutil.TryCloseWrite(app)
		}
	}()
}

func goldenAppRead(t *testing.T, app net.Conn, n int) []byte {
	t.Helper()
	_ = app.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = app.SetReadDeadline(time.Time{}) }()
	buf := make([]byte, n)
	if _, err := io.ReadFull(app, buf); err != nil {
		t.Fatalf("read %d bytes from session: %v", n, err)
	}
	return buf
}

func requireGoldenReplay(t *testing.T, ex goldenExchange, want string, rejected bool) {
	t.Helper()
	if ex.result != HandlePassThrough || ex.err != nil {
		t.Fatalf("result=%v err=%v reply=%q, want pass-through", ex.result, ex.err, ex.reply)
	}
	pc, ok := ex.conn.(*preBufferedConn)
	if !ok {
		t.Fatalf("pass-through conn is %T", ex.conn)
	}
	if got := string(pc.GetBufferedAndRecorded()); got != want {
		t.Fatalf("replay prefix\n got %q\nwant %q", got, want)
	}
	if pc.IsHTTPMaskRejected() != rejected {
		t.Fatalf("rejected=%v, want %v", pc.IsHTTPMaskRejected(), rejected)
	}
	if ex.reply != "" {
		t.Fatalf("pass-through wrote %q", ex.reply)
	}
}

func TestGoldenAuthorizeResponse(t *testing.T) {
	earlyRequest := base64.RawURLEncoding.EncodeToString([]byte("hello"))
	cases := []struct {
		name      string
		mode      string
		root      string
		target    string
		early     bool
		wantLen   int
		wantTrail string
	}{
		{name: "stream", mode: "stream", target: "/session", wantLen: 43, wantTrail: "\ncap=upload-seq"},
		{name: "poll", mode: "poll", target: "/session", wantLen: 43, wantTrail: "\ncap=upload-seq"},
		{name: "auto serves stream header", mode: "auto", target: "/session", wantLen: 43, wantTrail: "\ncap=upload-seq"},
		{name: "path root", mode: "stream", root: "/r", target: "/r/session", wantLen: 43, wantTrail: "\ncap=upload-seq"},
		{name: "early data", mode: "stream", target: "/session?ed=" + earlyRequest, early: true, wantLen: 53, wantTrail: "\ned=cmVzcA\ncap=upload-seq"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotEarly []byte
			opts := TunnelServerOptions{Mode: tc.mode, PathRoot: tc.root}
			if tc.early {
				opts.EarlyHandshake = &TunnelServerEarlyHandshake{Prepare: func(payload []byte) (*PreparedServerEarlyHandshake, error) {
					gotEarly = append([]byte(nil), payload...)
					return &PreparedServerEarlyHandshake{ResponsePayload: []byte("resp")}, nil
				}}
			}
			srv := NewTunnelServer(opts)
			tunnel := tc.mode
			if tunnel == "auto" {
				tunnel = "stream"
			}
			ex := runGoldenExchange(t, srv, goldenRequest("GET", tc.target, tunnel, ""), false)
			if ex.result != HandleStartTunnel || ex.err != nil {
				t.Fatalf("result=%v err=%v", ex.result, ex.err)
			}
			defer ex.conn.Close()

			m := goldenTokenPattern.FindStringSubmatch(ex.reply)
			if m == nil || len(m[1]) != 22 {
				t.Fatalf("token missing or malformed in %q", ex.reply)
			}
			defer srv.sessions.close(m[1])
			want := strings.Replace(goldenAuthorizeHeadFormat, "%d", strconv.Itoa(tc.wantLen), 1) + "token=" + m[1] + tc.wantTrail
			if ex.reply != want {
				t.Fatalf("authorize reply\n got %q\nwant %q", ex.reply, want)
			}
			if tc.early && string(gotEarly) != "hello" {
				t.Fatalf("early payload = %q", gotEarly)
			}
		})
	}
}

func TestGoldenAuthorizeFailures(t *testing.T) {
	earlyQuery := "/session?ed=" + base64.RawURLEncoding.EncodeToString([]byte("hello"))
	prepare := func(p *PreparedServerEarlyHandshake, err error) *TunnelServerEarlyHandshake {
		return &TunnelServerEarlyHandshake{Prepare: func([]byte) (*PreparedServerEarlyHandshake, error) { return p, err }}
	}
	cases := []struct {
		name   string
		early  *TunnelServerEarlyHandshake
		target string
		want   string
	}{
		{"prepare error is 404", prepare(nil, errors.New("denied")), earlyQuery, goldenReply404},
		{"wrap error is 500", prepare(&PreparedServerEarlyHandshake{WrapConn: func(net.Conn) (net.Conn, error) { return nil, errors.New("boom") }}, nil), earlyQuery, goldenReply500},
		{"undecodable ed is 400", nil, "/session?ed=!!!", goldenReply400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: "stream", EarlyHandshake: tc.early})
			ex := runGoldenExchange(t, srv, goldenRequest("GET", tc.target, "stream", ""), false)
			if ex.result != HandleDone || ex.reply != tc.want {
				t.Fatalf("result=%v reply=%q, want %q", ex.result, ex.reply, tc.want)
			}
			if srv.sessions.has("") || srv.sessions.len() != 0 {
				t.Fatalf("failed authorize leaked a session")
			}
		})
	}
}

func TestGoldenRejectedResponses(t *testing.T) {
	const upload = "/api/v1/upload"
	cases := []struct {
		name    string
		mode    string
		root    string
		request string
		want    string
	}{
		{"stream unknown path", "stream", "", goldenRequest("GET", "/nope", "stream", ""), goldenReply404},
		{"stream pull without token", "stream", "", goldenRequest("GET", "/stream", "stream", ""), goldenReply400},
		{"stream session with token", "stream", "", goldenRequest("GET", "/session?token=x", "stream", ""), goldenReply400},
		{"stream non-session allowed path", "stream", "", goldenRequest("GET", upload, "stream", ""), goldenReply400},
		{"stream unsupported method", "stream", "", goldenRequest("DELETE", "/session", "stream", ""), goldenReply400},
		{"stream pull unknown token", "stream", "", goldenRequest("GET", "/stream?token=nope", "stream", ""), goldenReply403},
		{"stream push unknown token", "stream", "", goldenRequest("POST", upload+"?seq=1&token=nope", "stream", "abc"), goldenReply403},
		{"stream push missing seq", "stream", "", goldenRequest("POST", upload+"?token=nope", "stream", "abc"), goldenReply400},
		{"stream push zero seq", "stream", "", goldenRequest("POST", upload+"?seq=0&token=nope", "stream", "abc"), goldenReply400},
		{"stream push bad length", "stream", "", goldenRequest("POST", upload+"?seq=1&token=nope", "stream", "", "Content-Length: abc"), goldenReply400},
		{"stream close unknown token", "stream", "", goldenRequest("POST", upload+"?token=nope&close=1", "stream", ""), goldenReply200},
		{"stream fin unknown token", "stream", "", goldenRequest("POST", upload+"?token=nope&fin=1", "stream", ""), goldenReply200},
		{"stream bad early data", "stream", "", goldenRequest("GET", "/session?ed=!!!", "stream", ""), goldenReply400},
		{"stream trailing slash", "stream", "", goldenRequest("GET", "/session/", "stream", ""), goldenReply404},

		{"poll pull without token is forbidden", "poll", "", goldenRequest("GET", "/stream", "poll", ""), goldenReply403},
		{"poll pull unknown token", "poll", "", goldenRequest("GET", "/stream?token=nope", "poll", ""), goldenReply403},
		{"poll push without token", "poll", "", goldenRequest("POST", upload, "poll", "abc"), goldenReply400MissingTok},
		{"poll post to other path", "poll", "", goldenRequest("POST", "/session", "poll", ""), goldenReply400},
		{"poll push unknown token", "poll", "", goldenRequest("POST", upload+"?seq=1&token=nope", "poll", "YWJj\n"), goldenReply403},
		{"poll push missing seq", "poll", "", goldenRequest("POST", upload+"?token=nope", "poll", "YWJj\n"), goldenReply400},
		{"poll push bad length", "poll", "", goldenRequest("POST", upload+"?seq=1&token=nope", "poll", "", "Content-Length: abc"), goldenReply400},
		{"poll close unknown token", "poll", "", goldenRequest("POST", upload+"?token=nope&close=1", "poll", ""), goldenReply200},
		{"poll fin unknown token", "poll", "", goldenRequest("POST", upload+"?token=nope&fin=1", "poll", ""), goldenReply200},
		{"poll unsupported method", "poll", "", goldenRequest("DELETE", "/session", "poll", ""), goldenReply400},

		{"poll server stream header", "poll", "", goldenRequest("GET", "/session", "stream", ""), goldenReply404},
		{"stream server poll header", "stream", "", goldenRequest("GET", "/session", "poll", ""), goldenReply404},
		{"legacy server stream header", "legacy", "", goldenRequest("GET", "/session", "stream", ""), goldenReply404},
		{"ws server stream header", "ws", "", goldenRequest("GET", "/session", "stream", ""), goldenReply404},
		{"unknown tunnel header", "auto", "", goldenRequest("GET", "/session", "bogus", ""), goldenReply404},
		{"stream server websocket upgrade", "stream", "", goldenRequest("GET", "/ws", "", "", "Upgrade: websocket", "Connection: Upgrade"), goldenReply404},

		{"root required for session", "stream", "/r", goldenRequest("GET", "/session", "stream", ""), goldenReply404},
		{"root alone", "stream", "/r", goldenRequest("GET", "/r", "stream", ""), goldenReply404},
		{"root with trailing slash", "stream", "/r", goldenRequest("GET", "/r/", "stream", ""), goldenReply404},
		{"root prefix is not a segment", "stream", "/r", goldenRequest("GET", "/rx/session", "stream", ""), goldenReply404},
		{"root pull unknown token", "poll", "/r", goldenRequest("GET", "/r/stream?token=nope", "poll", ""), goldenReply403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tc.mode, PathRoot: tc.root})
			ex := runGoldenExchange(t, srv, tc.request, false)
			if ex.result != HandleDone || ex.err != nil {
				t.Fatalf("result=%v err=%v reply=%q", ex.result, ex.err, ex.reply)
			}
			if ex.reply != tc.want {
				t.Fatalf("reply\n got %q\nwant %q", ex.reply, tc.want)
			}
		})
	}
}

func TestGoldenRejectedRequestsReplayWhenPassThroughOnReject(t *testing.T) {
	const upload = "/api/v1/upload"
	cases := []struct {
		name     string
		mode     string
		root     string
		request  string
		rejected bool
	}{
		{"mode mismatch", "poll", "", goldenRequest("GET", "/session", "stream", ""), true},
		{"legacy server", "legacy", "", goldenRequest("GET", "/session", "stream", ""), true},
		{"unknown path", "stream", "", goldenRequest("GET", "/nope", "stream", ""), true},
		{"missing root", "stream", "/r", goldenRequest("GET", "/session", "stream", ""), true},
		{"unsupported method on unknown path", "stream", "", goldenRequest("DELETE", "/x", "stream", ""), true},
		{"stream pull unknown token", "stream", "", goldenRequest("GET", "/stream?token=nope", "stream", ""), true},
		{"stream push unknown token with body", "stream", "", goldenRequest("POST", upload+"?seq=1&token=nope", "stream", "abc"), true},
		{"stream bad early data", "stream", "", goldenRequest("GET", "/session?ed=!!!", "stream", ""), true},
		{"poll pull without token", "poll", "", goldenRequest("GET", "/stream", "poll", ""), true},
		{"poll push without token", "poll", "", goldenRequest("POST", upload, "poll", "YWJj\n"), true},
		{"poll push unknown token with body", "poll", "", goldenRequest("POST", upload+"?seq=1&token=nope", "poll", "YWJj\n"), true},
		{"ws upgrade on stream server", "stream", "", goldenRequest("GET", "/ws", "", "", "Upgrade: websocket", "Connection: Upgrade"), true},
		{"plain http without tunnel header", "auto", "", goldenRequest("GET", "/", "", ""), false},
		{"plain http post without tunnel header", "stream", "", goldenRequest("POST", "/form", "", "a=b"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tc.mode, PathRoot: tc.root, PassThroughOnReject: true})
			ex := runGoldenExchange(t, srv, tc.request, false)
			requireGoldenReplay(t, ex, tc.request, tc.rejected)
		})
	}
}

func TestGoldenInvalidRequestsReplayVerbatim(t *testing.T) {
	cases := []struct {
		name    string
		request string
		eof     bool
	}{
		{"truncated header", "GET / HTTP/1.1\r\nBroken", true},
		{"short request line", "GET /x\r\n\r\n", false},
		{"fewer than four bytes", "GE", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: "auto", PassThroughOnReject: true})
			ex := runGoldenExchange(t, srv, tc.request, tc.eof)
			requireGoldenReplay(t, ex, tc.request, false)
		})
	}

	t.Run("empty connection", func(t *testing.T) {
		srv := NewTunnelServer(TunnelServerOptions{Mode: "auto"})
		client, server := net.Pipe()
		_ = client.Close()
		result, conn, err := srv.HandleConn(server)
		if result != HandleDone || conn != nil || !errors.Is(err, io.EOF) {
			t.Fatalf("result=%v conn=%v err=%v", result, conn, err)
		}
	})

	t.Run("non-http bytes keep the remainder readable", func(t *testing.T) {
		srv := NewTunnelServer(TunnelServerOptions{Mode: "auto"})
		client, server := net.Pipe()
		defer client.Close()
		defer server.Close()
		payload := []byte("\x16\x03\x01\x02\x00\x01\x00\x01\xfc\x03\x03")
		go func() { _, _ = client.Write(payload) }()
		result, conn, err := srv.HandleConn(server)
		if result != HandlePassThrough || err != nil {
			t.Fatalf("result=%v err=%v", result, err)
		}
		pc := conn.(*preBufferedConn)
		if got := pc.GetBufferedAndRecorded(); !bytes.Equal(got, payload[:4]) || pc.IsHTTPMaskRejected() {
			t.Fatalf("recorded=%q rejected=%v", got, pc.IsHTTPMaskRejected())
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("replayed %q err=%v", got, err)
		}
	})
}

func TestGoldenStreamAndPollDifferOnBadUploadBody(t *testing.T) {
	const upload = "/api/v1/upload"
	bad := func(tunnel, token string) string {
		return goldenRequest("POST", upload+"?seq=1&token="+token, tunnel, "", "Content-Length: abc")
	}

	streamSrv := NewTunnelServer(TunnelServerOptions{Mode: "stream", PassThroughOnReject: true})
	token, _ := openGoldenSession(t, streamSrv, "stream", "")
	ex := runGoldenExchange(t, streamSrv, bad("stream", token), false)
	if ex.result != HandleDone || ex.reply != goldenReply400 {
		t.Fatalf("stream: result=%v reply=%q, want bare 400", ex.result, ex.reply)
	}

	pollSrv := NewTunnelServer(TunnelServerOptions{Mode: "poll", PassThroughOnReject: true})
	token, _ = openGoldenSession(t, pollSrv, "poll", "")
	request := bad("poll", token)
	requireGoldenReplay(t, runGoldenExchange(t, pollSrv, request, false), request, true)
}

func TestGoldenUploadSequenceAndStatuses(t *testing.T) {
	const upload = "/api/v1/upload"
	for _, tunnel := range []string{"stream", "poll"} {
		t.Run(tunnel, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tunnel})
			token, app := openGoldenSession(t, srv, tunnel, "")
			encode := func(p string) string {
				if tunnel == "poll" {
					return base64.StdEncoding.EncodeToString([]byte(p)) + "\n"
				}
				return p
			}
			pushAsync := func(seq int, body string) func() goldenExchange {
				target := upload + "?seq=" + strconv.Itoa(seq) + "&token=" + token
				done := make(chan goldenExchange, 1)
				go func() { done <- runGoldenExchange(t, srv, goldenRequest("POST", target, tunnel, body), false) }()
				return func() goldenExchange { return <-done }
			}
			push := func(seq int, body string) goldenExchange { return pushAsync(seq, body)() }
			expectReply := func(ex goldenExchange, want string) {
				t.Helper()
				if ex.result != HandleDone || ex.reply != want {
					t.Fatalf("result=%v reply=%q, want %q", ex.result, ex.reply, want)
				}
			}
			delivered := func(seq int, body, want string) {
				t.Helper()
				wait := pushAsync(seq, body)
				if got := goldenAppRead(t, app, len(want)); string(got) != want {
					t.Fatalf("seq %d delivered %d bytes, want %d", seq, len(got), len(want))
				}
				expectReply(wait(), goldenReply200)
			}

			delivered(1, encode("abc"), "abc")
			// A retried sequence is acknowledged without delivering the payload twice.
			expectReply(push(1, encode("abc")), goldenReply200)
			delivered(2, encode("def"), "def")
			expectReply(push(9, encode("zzz")), goldenReply409)

			if tunnel == "poll" {
				delivered(3, "YWJj\nZGVm\n", "abcdef")
				expectReply(push(4, "***not base64***\n"), goldenReply400)
			} else {
				full := strings.Repeat("x", 1<<20)
				delivered(3, full, full)
				expectReply(push(4, full+"x"), goldenReply413)
			}

			// Closing the application side breaks the next write: 410, and the session is gone.
			_ = app.Close()
			expectReply(push(4, encode("late")), goldenReply410)
			expectReply(push(4, encode("late")), goldenReply403)
		})
	}
}

// A malformed upload body is answered in place in stream mode but handed back
// as a rejected request in poll mode when PassThroughOnReject is set.
func TestGoldenBadUploadBodyDiffersBetweenModes(t *testing.T) {
	const upload = "/api/v1/upload"
	for _, tunnel := range []string{"stream", "poll"} {
		t.Run(tunnel, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tunnel, PassThroughOnReject: true})
			token, _ := openGoldenSession(t, srv, tunnel, "")
			request := goldenRequest("POST", upload+"?seq=1&token="+token, tunnel, "", "Content-Length: abc")
			ex := runGoldenExchange(t, srv, request, false)
			if tunnel == "poll" {
				requireGoldenReplay(t, ex, request, true)
				return
			}
			if ex.result != HandleDone || ex.reply != goldenReply400 {
				t.Fatalf("result=%v reply=%q, want %q", ex.result, ex.reply, goldenReply400)
			}
		})
	}
}

func TestGoldenUploadBodyLimits(t *testing.T) {
	const upload = "/api/v1/upload"
	cases := []struct {
		name   string
		tunnel string
		body   string
		want   string
	}{
		{"stream at the cap", "stream", strings.Repeat("a", 1<<20), goldenReply200},
		{"stream over the cap", "stream", strings.Repeat("a", 1<<20+1), goldenReply413},
		{"poll bad base64", "poll", "!!!!\n", goldenReply400},
		{"poll blank lines are skipped", "poll", "\n\r\nYWJj\n\n", goldenReply200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tc.tunnel})
			token, app := openGoldenSession(t, srv, tc.tunnel, "")
			go func() { _, _ = io.Copy(io.Discard, app) }()
			ex := runGoldenExchange(t, srv, goldenRequest("POST", upload+"?seq=1&token="+token, tc.tunnel, tc.body), false)
			if ex.result != HandleDone || ex.reply != tc.want {
				t.Fatalf("result=%v reply=%.80q, want %q", ex.result, ex.reply, tc.want)
			}
		})
	}
}

func TestGoldenControlRequests(t *testing.T) {
	const upload = "/api/v1/upload"
	for _, tunnel := range []string{"stream", "poll"} {
		t.Run(tunnel+" fin half-closes uplink", func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tunnel})
			token, app := openGoldenSession(t, srv, tunnel, "")
			ex := runGoldenExchange(t, srv, goldenRequest("POST", upload+"?token="+token+"&fin=1", tunnel, ""), false)
			if ex.result != HandleDone || ex.reply != goldenReply200 {
				t.Fatalf("result=%v reply=%q", ex.result, ex.reply)
			}
			_ = app.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := app.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatalf("read after fin = %v, want EOF", err)
			}
			if !srv.sessions.has(token) {
				t.Fatalf("fin must keep the session for the downlink")
			}
		})
		t.Run(tunnel+" close drops the session", func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tunnel})
			token, _ := openGoldenSession(t, srv, tunnel, "")
			ex := runGoldenExchange(t, srv, goldenRequest("POST", upload+"?token="+token+"&close=1", tunnel, ""), false)
			if ex.result != HandleDone || ex.reply != goldenReply200 {
				t.Fatalf("result=%v reply=%q", ex.result, ex.reply)
			}
			if srv.sessions.has(token) {
				t.Fatalf("close must remove the session")
			}
		})
	}
}

func TestGoldenPullResponse(t *testing.T) {
	const eofTrailer = "0\r\nX-Sudoku-Stream-EOF: 1\r\n\r\n"
	cases := []struct {
		name     string
		tunnel   string
		downlink string
		eof      bool
		want     string
	}{
		{"stream data then idle", "stream", "hello", false, goldenPullHeader + "5\r\nhello\r\n" + "0\r\n\r\n"},
		{"stream idle", "stream", "", false, goldenPullHeader + "0\r\n\r\n"},
		{"stream data then eof", "stream", "hello", true, goldenPullHeader + "5\r\nhello\r\n" + eofTrailer},
		{"stream eof only", "stream", "", true, goldenPullHeader + eofTrailer},
		{"poll data then idle", "poll", "hello", false, goldenPullHeader + "8\r\naGVsbG8=\r\n1\r\n\n\r\n" + "1\r\n\n\r\n0\r\n\r\n"},
		{"poll idle keepalive", "poll", "", false, goldenPullHeader + "1\r\n\n\r\n0\r\n\r\n"},
		{"poll data then eof", "poll", "hello", true, goldenPullHeader + "8\r\naGVsbG8=\r\n1\r\n\n\r\n" + eofTrailer},
		{"poll eof only", "poll", "", true, goldenPullHeader + eofTrailer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tc.tunnel, PullReadTimeout: 200 * time.Millisecond})
			token, app := openGoldenSession(t, srv, tc.tunnel, "")
			goldenAppWrite(app, []byte(tc.downlink), tc.eof)

			ex := runGoldenExchange(t, srv, goldenRequest("GET", "/stream?token="+token, tc.tunnel, ""), false)
			if ex.result != HandleDone || ex.reply != tc.want {
				t.Fatalf("result=%v\n got %q\nwant %q", ex.result, ex.reply, tc.want)
			}
		})
	}
}

func TestGoldenStreamOneHandshake(t *testing.T) {
	srv := NewTunnelServer(TunnelServerOptions{Mode: "stream"})
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	request := goldenRequest("POST", "/data/sync", "stream", "5\r\nhello\r\n", "Transfer-Encoding: chunked")
	go func() { _, _ = client.Write([]byte(request)) }()

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

	header := make([]byte, len(goldenOneHeader))
	if _, err := io.ReadFull(client, header); err != nil || string(header) != goldenOneHeader {
		t.Fatalf("stream-one header %q err=%v", header, err)
	}
	h := <-done
	if h.result != HandleStartTunnel || h.err != nil {
		t.Fatalf("result=%v err=%v", h.result, h.err)
	}

	got := make([]byte, 5)
	if _, err := io.ReadFull(h.conn, got); err != nil || string(got) != "hello" {
		t.Fatalf("stream-one body %q err=%v", got, err)
	}
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = h.conn.Write([]byte("world"))
	}()
	wire := make([]byte, len("5\r\nworld\r\n"))
	if _, err := io.ReadFull(client, wire); err != nil || string(wire) != "5\r\nworld\r\n" {
		t.Fatalf("stream-one downlink %q err=%v", wire, err)
	}
	<-written
	go func() { _ = h.conn.(interface{ CloseWrite() error }).CloseWrite() }()
	tail := make([]byte, len("0\r\n\r\n"))
	if _, err := io.ReadFull(client, tail); err != nil || string(tail) != "0\r\n\r\n" {
		t.Fatalf("stream-one terminator %q err=%v", tail, err)
	}
}

func TestGoldenPathRoot(t *testing.T) {
	t.Run("normalize", func(t *testing.T) {
		for in, want := range map[string]string{
			"":       "",
			"/":      "",
			"abc":    "/abc",
			"/abc/":  "/abc",
			" abc ":  "/abc",
			"A_b-9":  "/A_b-9",
			"a/b":    "",
			"a b":    "",
			"a.b":    "",
			"../x":   "",
			"ab\x00": "",
		} {
			if got := normalizePathRoot(in); got != want {
				t.Errorf("normalizePathRoot(%q) = %q, want %q", in, got, want)
			}
		}
	})
	t.Run("strip", func(t *testing.T) {
		cases := []struct {
			root, path, want string
			ok               bool
		}{
			{"abc", "/abc/ws", "/ws", true},
			{"/abc/", "/abc/ws", "/ws", true},
			{"abc", "/abc/", "/", true},
			{"abc", "/abc", "", false},
			{"abc", "/abcd/ws", "", false},
			{"abc", "/ws", "", false},
			{"", "/ws", "/ws", true},
			{"a.b", "/ws", "/ws", true},
		}
		for _, tc := range cases {
			got, ok := stripPathRoot(tc.root, tc.path)
			if got != tc.want || ok != tc.ok {
				t.Errorf("stripPathRoot(%q, %q) = (%q, %v), want (%q, %v)", tc.root, tc.path, got, ok, tc.want, tc.ok)
			}
		}
	})
	t.Run("join", func(t *testing.T) {
		cases := []struct{ root, path, want string }{
			{"abc", "/ws", "/abc/ws"},
			{"abc", "ws", "/abc/ws"},
			{"abc", "", "/abc"},
			{"", "/ws", "/ws"},
			{"a.b", "/ws", "/ws"},
		}
		for _, tc := range cases {
			if got := joinPathRoot(tc.root, tc.path); got != tc.want {
				t.Errorf("joinPathRoot(%q, %q) = %q, want %q", tc.root, tc.path, got, tc.want)
			}
		}
	})
}
