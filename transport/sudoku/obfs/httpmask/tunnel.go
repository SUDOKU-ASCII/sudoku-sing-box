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
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

type TunnelMode string

const (
	TunnelModeLegacy TunnelMode = "legacy"
	TunnelModeStream TunnelMode = "stream"
	TunnelModePoll   TunnelMode = "poll"
	TunnelModeAuto   TunnelMode = "auto"
	TunnelModeWS     TunnelMode = "ws"
)

func normalizeTunnelMode(mode string) TunnelMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", string(TunnelModeLegacy):
		return TunnelModeLegacy
	case string(TunnelModeStream):
		return TunnelModeStream
	case string(TunnelModePoll):
		return TunnelModePoll
	case string(TunnelModeAuto):
		return TunnelModeAuto
	case string(TunnelModeWS):
		return TunnelModeWS
	default:
		// Be conservative: unknown => legacy
		return TunnelModeLegacy
	}
}

func multiplexEnabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto", "on":
		return true
	default:
		return false
	}
}

type HandleResult int

const (
	HandlePassThrough HandleResult = iota
	HandleStartTunnel
	HandleDone
)

type TunnelDialOptions struct {
	Mode         string
	TLSEnabled   bool   // when true, use HTTPS; when false, use HTTP (no port-based inference)
	HostOverride string // optional Host header / SNI host (without scheme); port inferred from ServerAddress
	// PathRoot is an optional first-level path prefix for all HTTP tunnel endpoints.
	// Example: "aabbcc" => "/aabbcc/session", "/aabbcc/api/v1/upload", ...
	PathRoot string
	// AuthKey enables the optional WebSocket HMAC anti-probing layer. Stream and
	// poll sessions authenticate with the Sudoku handshake and replay guard.
	AuthKey string
	// Upgrade optionally wraps the raw tunnel conn and/or writes a small prelude before DialTunnel returns.
	// It is called with the raw tunnel conn; if it returns a non-nil conn, that conn is returned by DialTunnel.
	//
	// Upgrade is primarily used to reduce RTT by sending initial bytes (e.g. protocol handshake) while the
	// HTTP tunnel is still establishing.
	Upgrade func(raw net.Conn) (net.Conn, error)
	// EarlyHandshake folds the protocol handshake into the HTTP/WS setup round trip.
	// When the server accepts the early payload, DialTunnel returns a conn that is already post-handshake.
	// When the server does not echo early data, DialTunnel falls back to Upgrade.
	EarlyHandshake *ClientEarlyHandshake
	// Multiplex controls whether DialTunnel reuses underlying HTTP connections (keep-alive / h2).
	// Values: "off" disables global reuse; "auto"/"on" enables it. Empty defaults to "auto".
	Multiplex string
	// TransportPool scopes HTTP transport reuse to a caller-owned pool.
	// When nil, multiplexing uses the package-global cache.
	TransportPool *TransportPool
	// DialContext overrides raw TCP/TLS dialing when supplied by the embedding client.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

type ClientEarlyHandshake struct {
	RequestPayload []byte
	HandleResponse func(payload []byte) error
	Ready          func() bool
	WrapConn       func(raw net.Conn) (net.Conn, error)
}

type TunnelServerEarlyHandshake struct {
	Prepare func(payload []byte) (*PreparedServerEarlyHandshake, error)
}

type PreparedServerEarlyHandshake struct {
	ResponsePayload []byte
	WrapConn        func(raw net.Conn) (net.Conn, error)
	UserHash        string
}

type earlyHandshakeMeta interface {
	HTTPMaskEarlyHandshakeUserHash() string
	HTTPMaskEarlyHandshakeUplinkPacked() bool
}

type obfsMeta interface {
	SudokuUplinkPacked() bool
}

type earlyHandshakeConn struct {
	net.Conn
	userHash     string
	uplinkPacked bool
}

func (c *earlyHandshakeConn) CloseWrite() error {
	if c == nil {
		return nil
	}
	return connutil.TryCloseWrite(c.Conn)
}

func (c *earlyHandshakeConn) CloseRead() error {
	if c == nil {
		return nil
	}
	return connutil.TryCloseRead(c.Conn)
}

func (c *earlyHandshakeConn) HTTPMaskEarlyHandshakeUserHash() string {
	if c == nil {
		return ""
	}
	return c.userHash
}

func (c *earlyHandshakeConn) HTTPMaskEarlyHandshakeUplinkPacked() bool {
	if c == nil {
		return false
	}
	return c.uplinkPacked
}

func wrapEarlyHandshakeConn(conn net.Conn, userHash string, uplinkPacked bool) net.Conn {
	if conn == nil {
		return nil
	}
	return &earlyHandshakeConn{Conn: conn, userHash: userHash, uplinkPacked: uplinkPacked}
}

func wrappedConnUplinkPacked(conn net.Conn) bool {
	if conn == nil {
		return false
	}
	v, ok := conn.(obfsMeta)
	if !ok {
		return false
	}
	return v.SudokuUplinkPacked()
}

func EarlyHandshakeUserHash(conn net.Conn) (string, bool) {
	if conn == nil {
		return "", false
	}
	v, ok := conn.(earlyHandshakeMeta)
	if !ok {
		return "", false
	}
	return v.HTTPMaskEarlyHandshakeUserHash(), true
}

func EarlyHandshakeUplinkPacked(conn net.Conn) (bool, bool) {
	if conn == nil {
		return false, false
	}
	v, ok := conn.(earlyHandshakeMeta)
	if !ok {
		return false, false
	}
	return v.HTTPMaskEarlyHandshakeUplinkPacked(), true
}

// DialTunnel establishes a bidirectional stream over HTTP:
//   - stream: split-stream (authorize + upload/pull endpoints; CDN-friendly)
//   - poll: authorize + push/pull polling tunnel (base64 framed)
//   - auto: try stream then fall back to poll
//
// The returned net.Conn carries the raw Sudoku stream (no HTTP headers).
func DialTunnel(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	switch normalizeTunnelMode(opts.Mode) {
	case TunnelModeStream:
		return dialStreamFn(ctx, serverAddress, opts)
	case TunnelModePoll:
		return dialPollFn(ctx, serverAddress, opts)
	case TunnelModeWS:
		return dialWSTunnel(ctx, serverAddress, opts)
	case TunnelModeAuto:
		return dialAutoTunnel(ctx, serverAddress, opts)
	}
	return nil, fmt.Errorf("legacy mode does not use http tunnel")
}

func dialWSTunnel(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	c, err := dialWS(ctx, serverAddress, opts)
	if err != nil {
		return nil, err
	}
	return upgradeDialedConn(c, opts)
}

// upgradeDialedConn runs the early-handshake wrapper (when the handshake is ready) or Upgrade on a freshly dialed
// conn. A nil result from the wrapper keeps raw; on error raw is closed.
func upgradeDialedConn(raw net.Conn, opts TunnelDialOptions) (net.Conn, error) {
	wrap := opts.Upgrade
	if eh := opts.EarlyHandshake; eh != nil && eh.WrapConn != nil && (eh.Ready == nil || eh.Ready()) {
		wrap = eh.WrapConn
	}
	if wrap == nil {
		return raw, nil
	}
	upgraded, err := wrap(raw)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	if upgraded == nil {
		return raw, nil
	}
	return upgraded, nil
}

// dialAutoTunnel tries stream first and falls back to poll.
func dialAutoTunnel(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	// Stream can hang on CDNs that buffer uploads until the request body completes;
	// the short leash leaves time to fall back within the caller's deadline.
	streamCtx, cancelStream := context.WithTimeout(ctx, 3*time.Second)
	c, errStream := dialStreamFn(streamCtx, serverAddress, opts)
	cancelStream()
	if errStream == nil {
		return c, nil
	}
	c, errPoll := dialPollFn(ctx, serverAddress, opts)
	if errPoll == nil {
		return c, nil
	}
	return nil, fmt.Errorf("auto tunnel failed: stream: %v; poll: %w", errStream, errPoll)
}

var (
	dialStreamFn = dialStream
	dialPollFn   = dialPoll
)
