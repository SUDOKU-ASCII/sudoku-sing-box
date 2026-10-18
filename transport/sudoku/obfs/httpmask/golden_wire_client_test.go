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
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type goldenCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *goldenCapture) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

type goldenRecordingConn struct {
	net.Conn
	capture *goldenCapture
}

func (c *goldenRecordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.capture.mu.Lock()
		c.capture.buf.Write(p[:n])
		c.capture.mu.Unlock()
	}
	return n, err
}

// startGoldenRecordingServer serves srv on loopback and records every byte each
// client connection sends.
func startGoldenRecordingServer(t *testing.T, srv *TunnelServer) (addr string, captures func() []*goldenCapture, tunnels <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var (
		mu    sync.Mutex
		taken []*goldenCapture
	)
	tunnelCh := make(chan net.Conn, 4)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			capture := &goldenCapture{}
			mu.Lock()
			taken = append(taken, capture)
			mu.Unlock()
			go func(conn net.Conn) {
				result, c, err := srv.HandleConn(&goldenRecordingConn{Conn: conn, capture: capture})
				switch {
				case err != nil:
					_ = conn.Close()
				case result == HandleStartTunnel:
					tunnelCh <- c
				case result == HandlePassThrough:
					_ = c.Close()
				}
			}(raw)
		}
	}()
	return ln.Addr().String(), func() []*goldenCapture {
		mu.Lock()
		defer mu.Unlock()
		return append([]*goldenCapture(nil), taken...)
	}, tunnelCh
}

// describeGoldenRequests reduces captured client requests to a sorted set of
// "METHOD URI | selected headers | body" lines with the session token masked.
func describeGoldenRequests(t *testing.T, captures []*goldenCapture, addr string) []string {
	t.Helper()
	seen := map[string]bool{}
	token := ""
	var parsed []*http.Request
	var bodies []string
	for _, c := range captures {
		raw := c.bytes()
		if len(raw) == 0 {
			continue
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
		if err != nil {
			t.Fatalf("captured request is not valid HTTP: %v\n%q", err, raw)
		}
		body, _ := io.ReadAll(req.Body)
		parsed = append(parsed, req)
		bodies = append(bodies, string(body))
		if tok := req.URL.Query().Get("token"); tok != "" {
			token = tok
		}
	}
	if token == "" {
		t.Fatalf("no request carried a session token")
	}
	for i, req := range parsed {
		for _, name := range []string{"User-Agent", "Accept", "Accept-Language"} {
			if req.Header.Get(name) == "" {
				t.Errorf("%s %s: missing %s", req.Method, req.URL.Path, name)
			}
		}
		if req.Host != addr {
			t.Errorf("%s %s: Host = %q, want %q", req.Method, req.URL.Path, req.Host, addr)
		}
		line := strings.Join([]string{
			req.Method + " " + strings.ReplaceAll(req.RequestURI, token, "TOKEN"),
			"tunnel=" + req.Header.Get("X-Sudoku-Tunnel"),
			"cache-control=" + req.Header.Get("Cache-Control"),
			"pragma=" + req.Header.Get("Pragma"),
			"connection=" + req.Header.Get("Connection"),
			"content-type=" + req.Header.Get("Content-Type"),
			"body=" + strconv.Quote(bodies[i]),
		}, " | ")
		seen[line] = true
	}
	out := make([]string, 0, len(seen))
	for line := range seen {
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

func TestGoldenClientRequests(t *testing.T) {
	const common = " | cache-control=no-cache | pragma=no-cache | connection=keep-alive"
	cases := []struct {
		mode string
		want []string
	}{
		{"stream", []string{
			"GET /r/session | tunnel=stream" + common + " | content-type= | body=\"\"",
			"GET /r/stream?token=TOKEN | tunnel=stream" + common + " | content-type= | body=\"\"",
			"POST /r/api/v1/upload?seq=1&token=TOKEN | tunnel=stream" + common + " | content-type=application/octet-stream | body=\"hello\"",
			"POST /r/api/v1/upload?token=TOKEN&close=1 | tunnel=stream" + common + " | content-type= | body=\"\"",
			"POST /r/api/v1/upload?token=TOKEN&fin=1 | tunnel=stream" + common + " | content-type= | body=\"\"",
		}},
		{"poll", []string{
			"GET /r/session | tunnel=poll" + common + " | content-type= | body=\"\"",
			"GET /r/stream?token=TOKEN | tunnel=poll" + common + " | content-type= | body=\"\"",
			"POST /r/api/v1/upload?seq=1&token=TOKEN | tunnel=poll" + common + " | content-type=text/plain | body=\"aGVsbG8=\\n\"",
			"POST /r/api/v1/upload?token=TOKEN&close=1 | tunnel=poll" + common + " | content-type= | body=\"\"",
			"POST /r/api/v1/upload?token=TOKEN&fin=1 | tunnel=poll" + common + " | content-type= | body=\"\"",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			srv := NewTunnelServer(TunnelServerOptions{Mode: tc.mode, PathRoot: "r", PullReadTimeout: 50 * time.Millisecond, SessionTTL: 5 * time.Second})
			addr, captures, tunnels := startGoldenRecordingServer(t, srv)

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			conn, err := DialTunnel(ctx, addr, TunnelDialOptions{Mode: tc.mode, PathRoot: "r", Multiplex: "off"})
			if err != nil {
				t.Fatalf("DialTunnel: %v", err)
			}
			var tunnel net.Conn
			select {
			case tunnel = <-tunnels:
			case <-time.After(5 * time.Second):
				t.Fatal("server never started a tunnel")
			}
			defer tunnel.Close()

			if _, err := conn.Write([]byte("hello")); err != nil {
				t.Fatalf("client write: %v", err)
			}
			got := make([]byte, 5)
			_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(tunnel, got); err != nil || string(got) != "hello" {
				t.Fatalf("server read %q err=%v", got, err)
			}
			if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
				t.Fatalf("CloseWrite: %v", err)
			}
			_ = tunnel.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := tunnel.Read(make([]byte, 1)); !errorsIsEOF(err) {
				t.Fatalf("server read after fin = %v, want EOF", err)
			}
			go func() { _, _ = tunnel.Write([]byte("pong")) }()
			reply := make([]byte, 4)
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "pong" {
				t.Fatalf("client read %q err=%v", reply, err)
			}
			_ = conn.Close()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if srv.sessions.len() == 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}

			seen := strings.Join(describeGoldenRequests(t, captures(), addr), "\n")
			sort.Strings(tc.want)
			if want := strings.Join(tc.want, "\n"); seen != want {
				t.Fatalf("client requests\n got:\n%s\nwant:\n%s", seen, want)
			}
		})
	}
}

func errorsIsEOF(err error) bool { return err == io.EOF }

func TestGoldenUploadURLPutsSequenceFirst(t *testing.T) {
	const push = "http://golden.test:80/r/api/v1/upload?token=T-k_9"
	if got, want := uploadURL(push, 12), "http://golden.test:80/r/api/v1/upload?seq=12&token=T-k_9"; got != want {
		t.Fatalf("uploadURL = %q, want %q", got, want)
	}
}

// TestUploadURL_MatchesQueryEncoding pins the string-built URL to what parsing and re-encoding the query yields.
func TestUploadURL_MatchesQueryEncoding(t *testing.T) {
	for _, token := range []string{"T-k_9", "a+b/c=", "sp ace&x=y", "é"} {
		pushURL := "https://h.test/root/api/v1/upload?token=" + url.QueryEscape(token)
		u, err := url.Parse(pushURL)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("seq", "7")
		u.RawQuery = q.Encode()
		if got := uploadURL(pushURL, 7); got != u.String() {
			t.Fatalf("token %q: uploadURL = %q, want %q", token, got, u.String())
		}
	}
}
