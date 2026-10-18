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
	crand "crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type TunnelServerOptions struct {
	Mode string
	// PathRoot is an optional first-level path prefix for all HTTP tunnel endpoints.
	// Example: "aabbcc" => "/aabbcc/session", "/aabbcc/api/v1/upload", ...
	PathRoot string
	// AuthKey enables the optional WebSocket HMAC anti-probing layer. Stream and
	// poll sessions do not use a second HTTP-level authentication protocol.
	AuthKey string
	// AuthSkew controls allowed clock skew / replay window for AuthKey. 0 uses a conservative default.
	AuthSkew time.Duration
	// PassThroughOnReject controls how the server handles "recognized but rejected" tunnel requests
	// (e.g., wrong mode / wrong path / invalid token). When true, the request bytes are replayed back
	// to the caller as HandlePassThrough to allow higher-level fallback handling.
	PassThroughOnReject bool
	// PullReadTimeout controls how long the server long-poll waits for tunnel downlink data before replying.
	PullReadTimeout time.Duration
	// SessionTTL is a best-effort TTL to prevent leaked sessions. 0 uses a conservative default.
	SessionTTL time.Duration
	// EarlyHandshake optionally folds the protocol handshake into the initial HTTP/WS round trip.
	EarlyHandshake *TunnelServerEarlyHandshake
}

type TunnelServer struct {
	mode                TunnelMode
	pathRoot            string
	passThroughOnReject bool
	// auth is used only by the WebSocket transport.
	auth *tunnelAuth

	pullReadTimeout time.Duration
	earlyHandshake  *TunnelServerEarlyHandshake

	sessions *sessionStore
}

const tunnelHeaderReadTimeout = 15 * time.Second

func NewTunnelServer(opts TunnelServerOptions) *TunnelServer {
	mode := normalizeTunnelMode(opts.Mode)
	pathRoot := normalizePathRoot(opts.PathRoot)
	auth := newTunnelAuth(opts.AuthKey, opts.AuthSkew)
	timeout := opts.PullReadTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ttl := opts.SessionTTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &TunnelServer{
		mode:                mode,
		pathRoot:            pathRoot,
		passThroughOnReject: opts.PassThroughOnReject,
		auth:                auth,
		pullReadTimeout:     timeout,
		earlyHandshake:      opts.EarlyHandshake,
		sessions:            newSessionStore(ttl),
	}
}

// HandleConn inspects rawConn. If it is an HTTP tunnel request (stream/poll), it is handled here and:
//   - returns HandleStartTunnel + a net.Conn that carries the raw Sudoku stream (stream or poll session pipe)
//   - or returns HandleDone if the HTTP request is a poll control request (push/pull) and no Sudoku handshake should run on this TCP conn
//
// If it is not an HTTP tunnel request (or server mode is legacy), it returns HandlePassThrough with a conn that replays any pre-read bytes.
func (s *TunnelServer) HandleConn(rawConn net.Conn) (HandleResult, net.Conn, error) {
	if rawConn == nil {
		return HandleDone, nil, errors.New("nil conn")
	}

	passThrough := func(prefix []byte) (HandleResult, net.Conn, error) {
		return HandlePassThrough, newPreBufferedConn(rawConn, prefix), nil
	}
	passThroughRejected := func(prefix []byte) (HandleResult, net.Conn, error) {
		return HandlePassThrough, newRejectedPreBufferedConn(rawConn, prefix), nil
	}
	rejectOr404 := func(prefix []byte) (HandleResult, net.Conn, error) {
		if s.passThroughOnReject {
			return passThroughRejected(prefix)
		}
		return replyDone(rawConn, http.StatusNotFound, "not found")
	}

	// Preconnected HTTP sockets may wait for the client to assign their first
	// request after the TCP/TLS handshake. Keep this bounded, but longer than
	// the client's preconnect lease so high-RTT links do not turn a prepared
	// socket into a reset connection before net/http can use it.
	_ = rawConn.SetReadDeadline(time.Now().Add(tunnelHeaderReadTimeout))
	var first [4]byte
	n, err := io.ReadFull(rawConn, first[:])
	if err != nil {
		_ = rawConn.SetReadDeadline(time.Time{})
		// Even if short-read, preserve bytes for downstream handlers.
		if n > 0 {
			return passThrough(first[:n])
		}
		return HandleDone, nil, err
	}
	pc := newPreBufferedConn(rawConn, first[:])
	br := bufio.NewReader(pc)

	if !LooksLikeHTTPRequestStart(first[:]) {
		_ = rawConn.SetReadDeadline(time.Time{})
		return HandlePassThrough, pc, nil
	}

	req, unparsed, err := readHTTPRequest(rawConn, br, first[:])
	_ = rawConn.SetReadDeadline(time.Time{})
	if err != nil {
		// Not a valid HTTP request; hand it back to the legacy path with replay.
		return passThrough(unparsed)
	}

	tunnelHeader := TunnelMode(strings.ToLower(strings.TrimSpace(req.tunnel)))
	if req.isWebSocketUpgrade() {
		tunnelHeader = TunnelModeWS
	}
	if tunnelHeader == "" {
		// The explicit mode header is part of the stream/poll protocol. A request
		// without it belongs to the normal HTTP fallback path.
		return passThrough(req.replayPrefix())
	}

	if s.mode == TunnelModeLegacy {
		return rejectOr404(req.replayPrefix())
	}

	switch tunnelHeader {
	case TunnelModeStream:
		if s.mode != TunnelModeStream && s.mode != TunnelModeAuto {
			return rejectOr404(req.replayPrefix())
		}
		return s.handleSession(req, streamCodec)
	case TunnelModePoll:
		if s.mode != TunnelModePoll && s.mode != TunnelModeAuto {
			return rejectOr404(req.replayPrefix())
		}
		return s.handleSession(req, pollCodec)
	case TunnelModeWS:
		if s.mode != TunnelModeWS && s.mode != TunnelModeAuto {
			return rejectOr404(req.replayPrefix())
		}
		return s.handleWS(req)
	default:
		return rejectOr404(req.replayPrefix())
	}
}

// rejectOrRespond answers a rejected request with the given status, or replays it for pass-through fallback.
// It must run before the request body is consumed.
func (s *TunnelServer) rejectOrRespond(req *tunnelRequest, code int, body string) (HandleResult, net.Conn, error) {
	if s.passThroughOnReject {
		return HandlePassThrough, newRejectedPreBufferedConn(req.conn, req.replayPrefix()), nil
	}
	return replyDone(req.conn, code, body)
}

func (s *TunnelServer) sessionAuthorize(req *tunnelRequest, earlyPayload []byte) (HandleResult, net.Conn, error) {
	rawConn := req.conn
	token, err := newSessionToken()
	if err != nil {
		return replyDone(rawConn, http.StatusInternalServerError, "internal error")
	}

	c1, c2 := newHalfPipe()
	outConn := net.Conn(c1)
	var responsePayload []byte
	var userHash string
	if len(earlyPayload) > 0 && s.earlyHandshake != nil && s.earlyHandshake.Prepare != nil {
		prepared, err := s.earlyHandshake.Prepare(earlyPayload)
		if err != nil {
			_ = c1.Close()
			_ = c2.Close()
			return s.rejectOrRespond(req, http.StatusNotFound, "not found")
		}
		responsePayload = prepared.ResponsePayload
		userHash = prepared.UserHash
		if prepared.WrapConn != nil {
			wrapped, err := prepared.WrapConn(c1)
			if err != nil {
				_ = c1.Close()
				_ = c2.Close()
				return replyDone(rawConn, http.StatusInternalServerError, "internal error")
			}
			if wrapped != nil {
				outConn = wrapEarlyHandshakeConn(wrapped, userHash, wrappedConnUplinkPacked(wrapped))
			}
		}
	}

	s.sessions.add(token, c2)

	if err := writeTokenHTTPResponse(rawConn, token, responsePayload); err != nil {
		s.sessions.close(token)
		_ = rawConn.Close()
		return HandleDone, nil, err
	}
	_ = rawConn.Close()
	return HandleStartTunnel, outConn, nil
}

func newSessionToken() (string, error) {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
