package sudoku

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	internalconfig "github.com/sagernet/sing-box/transport/sudoku/internal/config"
	internalprotocol "github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
	internalusers "github.com/sagernet/sing-box/transport/sudoku/internal/users"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
)

type HTTPMaskTunnelServer struct {
	cfg   *ProtocolConfig
	ts    *httpmask.TunnelServer
	users *internalusers.Registry
}

func (s *HTTPMaskTunnelServer) Close() error { return nil }

// UserRejectedError reports a handshaked client that the users whitelist turned away.
type UserRejectedError struct {
	// User is the alias of a whitelisted user, or the handshake hash for an unknown one;
	// a private key never ends up here.
	User   string
	Reason string
}

func (e *UserRejectedError) Error() string {
	return fmt.Sprintf("[User:%s] rejected: %s", e.User, e.Reason)
}

// newUserRegistry validates and normalizes the whitelist; nil means no whitelist.
func newUserRegistry(users []UserConfig) (*internalusers.Registry, error) {
	if len(users) == 0 {
		return nil, nil
	}
	cfg := &internalconfig.Config{Users: append([]UserConfig(nil), users...)}
	if err := cfg.Finalize(); err != nil {
		return nil, err
	}
	return internalusers.NewRegistry(cfg.Users)
}

// userWireWrap counts the wire bytes of whitelisted users inside the early HTTPMask handshake,
// where the raw stream is not visible to the caller.
func userWireWrap(reg *internalusers.Registry) func(string, net.Conn) net.Conn {
	if reg == nil {
		return nil
	}
	return func(userHash string, raw net.Conn) net.Conn {
		if u := reg.Lookup(userHash); u != nil && !u.Blocked {
			return u.WrapWire(raw)
		}
		return raw
	}
}

func userRejectReason(user *internalusers.User) string {
	switch {
	case user == nil:
		return "not in users whitelist"
	case user.Blocked:
		return "blocked"
	case user.Exceeded():
		return "data quota exceeded"
	}
	return ""
}

// HasUsers reports whether a users whitelist is enforced.
func (s *HTTPMaskTunnelServer) HasUsers() bool { return s != nil && s.users != nil }

// UserLabel returns the alias of the whitelisted user behind a handshake hash, or the hash itself.
func (s *HTTPMaskTunnelServer) UserLabel(userHash string) string {
	if s != nil {
		if u := s.users.Lookup(userHash); u != nil {
			return u.Alias
		}
	}
	return userHash
}

// UserStat is a point-in-time view of one whitelisted user, for monitoring.
type UserStat struct {
	Name     string
	Blocked  bool
	Exceeded bool
	Conns    int    // open connections
	Up, Down uint64 // wire bytes from and to the client since start
	Used     uint64 // wire bytes this month, counted against Limit
	Limit    uint64 // bytes, 0 = unlimited
}

// UserStats lists the whitelisted users in config order.
func (s *HTTPMaskTunnelServer) UserStats() []UserStat {
	if s == nil || s.users == nil {
		return nil
	}
	stats := s.users.Stats()
	result := make([]UserStat, len(stats))
	for i, stat := range stats {
		result[i] = UserStat{
			Name:     stat.Name,
			Blocked:  stat.Blocked,
			Exceeded: stat.Exceeded,
			Conns:    stat.Conns,
			Up:       stat.Up,
			Down:     stat.Down,
			Used:     stat.Used,
			Limit:    stat.Limit,
		}
	}
	return result
}

// TrackUsage restores the users' monthly traffic, saves it periodically and resets it when a
// new month begins. The returned stop function saves once more and must be called on shutdown.
func (s *HTTPMaskTunnelServer) TrackUsage(ctx context.Context, path string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	return s.users.TrackUsage(ctx, path)
}

func NewHTTPMaskTunnelServer(cfg *ProtocolConfig) (*HTTPMaskTunnelServer, error) {
	if cfg == nil {
		return &HTTPMaskTunnelServer{}, nil
	}
	reg, err := newUserRegistry(cfg.Users)
	if err != nil {
		return nil, fmt.Errorf("users: %w", err)
	}

	var ts *httpmask.TunnelServer
	if !cfg.DisableHTTPMask {
		switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
		case "stream", "poll", "auto", "ws":
			ts = httpmask.NewTunnelServer(httpmask.TunnelServerOptions{
				Mode:                cfg.HTTPMaskMode,
				PathRoot:            cfg.HTTPMaskPathRoot,
				AuthKey:             cfg.Key,
				PassThroughOnReject: shouldPassThroughRejectedHTTPMask(cfg),
				EarlyHandshake: internaltunnel.NewHTTPMaskServerEarlyHandshake(internaltunnel.EarlyCodecConfig{
					PSK:                cfg.Key,
					AEAD:               cfg.AEADMethod,
					EnablePureDownlink: cfg.EnablePureDownlink,
					PaddingMin:         cfg.PaddingMin,
					PaddingMax:         cfg.PaddingMax,
					WireWrap:           userWireWrap(reg),
				}, cfg.tableCandidates(), nil),
			})
		}
	}

	return &HTTPMaskTunnelServer{cfg: cfg, ts: ts, users: reg}, nil
}

func shouldPassThroughRejectedHTTPMask(cfg *ProtocolConfig) bool {
	if cfg == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SuspiciousAction)) {
	case "", "fallback":
		return strings.TrimSpace(cfg.FallbackAddress) != ""
	case "silent":
		return true
	default:
		return false
	}
}

func ServerHandshake(rawConn net.Conn, cfg *ProtocolConfig) (*ServerSession, error) {
	conn, session, targetAddr, userHash, payload, err := ServerHandshakeSessionAutoWithUserHash(rawConn, cfg)
	if err != nil {
		return nil, err
	}
	return &ServerSession{
		Conn:     conn,
		Type:     session,
		Target:   targetAddr,
		UserHash: userHash,
		Payload:  payload,
	}, nil
}

func ServerHandshakeSessionAutoWithUserHash(rawConn net.Conn, cfg *ProtocolConfig) (net.Conn, SessionKind, string, string, []byte, error) {
	if cfg == nil {
		return nil, SessionForward, "", "", nil, fmt.Errorf("config is required")
	}
	// A registry built here lives for this one handshake, so data quotas are not shared across
	// calls; HTTPMaskTunnelServer keeps one registry for the lifetime of the listener.
	reg, err := newUserRegistry(cfg.Users)
	if err != nil {
		return nil, SessionForward, "", "", nil, fmt.Errorf("users: %w", err)
	}
	return serverHandshake(rawConn, cfg, reg)
}

func serverHandshake(rawConn net.Conn, cfg *ProtocolConfig, reg *internalusers.Registry) (net.Conn, SessionKind, string, string, []byte, error) {
	if err := cfg.Validate(); err != nil {
		return nil, SessionForward, "", "", nil, err
	}

	// Early-handshake conns are already wire-wrapped inside WireWrap; everything else is counted here.
	handshakeConn := rawConn
	var wire *internalusers.WireConn
	if reg != nil {
		if _, early := httpmask.EarlyHandshakeUserHash(rawConn); !early {
			wire = internalusers.NewWireConn(rawConn)
			handshakeConn = wire
		}
	}

	conn, meta, err := internaltunnel.ServerHandshake(handshakeConn, toInternalConfig(cfg), cfg.tableCandidates())
	if err != nil {
		return nil, SessionForward, "", "", nil, err
	}
	userHash := ""
	if meta != nil {
		userHash = meta.UserHash
	}
	if reg != nil {
		user := reg.Lookup(userHash)
		if reason := userRejectReason(user); reason != "" {
			who := userHash
			if user != nil {
				who = user.Alias
			}
			_ = conn.Close()
			return nil, SessionForward, "", "", nil, &UserRejectedError{User: who, Reason: reason}
		}
		if wire != nil {
			wire.Bind(user)
		}
		conn = user.WrapPayload(conn)
	}

	session, targetAddr, payload, err := readServerSession(conn)
	if err != nil {
		_ = conn.Close()
		return nil, SessionForward, "", "", nil, err
	}
	return conn, session, targetAddr, userHash, payload, nil
}

func (s *HTTPMaskTunnelServer) HandleConnSessionAutoWithUserHash(rawConn net.Conn) (net.Conn, SessionKind, string, string, []byte, bool, error) {
	if s == nil || s.cfg == nil {
		return nil, SessionForward, "", "", nil, false, nil
	}
	if s.ts == nil {
		return handledServerHandshake(rawConn, s.cfg, s.users)
	}

	res, c, err := s.ts.HandleConn(rawConn)
	if err != nil {
		return nil, SessionForward, "", "", nil, true, err
	}

	switch res {
	case httpmask.HandleDone:
		return nil, SessionForward, "", "", nil, true, nil
	case httpmask.HandlePassThrough:
		if rejected, ok := c.(interface{ IsHTTPMaskRejected() bool }); ok && rejected.IsHTTPMaskRejected() {
			return nil, SessionForward, "", "", nil, true, &internaltunnel.SuspiciousError{
				Err:  fmt.Errorf("httpmask request rejected"),
				Conn: newFallbackReplayConn(c, rawConn),
			}
		}
		return handledServerHandshakeWithFallbackConn(c, s.cfg, s.users, c, true)
	case httpmask.HandleStartTunnel:
		inner := *s.cfg
		inner.DisableHTTPMask = true
		return handledServerHandshakeWithFallbackConn(c, &inner, s.users, nil, false)
	default:
		return nil, SessionForward, "", "", nil, true, nil
	}
}

func handledServerHandshake(rawConn net.Conn, cfg *ProtocolConfig, reg *internalusers.Registry) (net.Conn, SessionKind, string, string, []byte, bool, error) {
	return handledServerHandshakeWithFallbackConn(rawConn, cfg, reg, rawConn, true)
}

func handledServerHandshakeWithFallbackConn(rawConn net.Conn, cfg *ProtocolConfig, reg *internalusers.Registry, fallbackStream net.Conn, allowFallback bool) (net.Conn, SessionKind, string, string, []byte, bool, error) {
	conn, session, target, userHash, payload, err := serverHandshake(rawConn, cfg, reg)
	if err != nil {
		if !allowFallback && rawConn != nil {
			_ = rawConn.Close()
		}
		var suspErr *internaltunnel.SuspiciousError
		if errors.As(err, &suspErr) {
			if allowFallback {
				suspErr.Conn = newFallbackReplayConn(suspErr.Conn, fallbackStream)
			} else {
				err = suspErr.Err
			}
		}
	}
	return conn, session, target, userHash, payload, true, err
}

type fallbackReplayConn struct {
	net.Conn
	recorder interface {
		GetBufferedAndRecorded() []byte
	}
}

func newFallbackReplayConn(recorderConn net.Conn, streamConn net.Conn) net.Conn {
	if streamConn == nil {
		streamConn = recorderConn
	}
	if streamConn == nil {
		return nil
	}
	recorder, _ := recorderConn.(interface {
		GetBufferedAndRecorded() []byte
	})
	return &fallbackReplayConn{
		Conn:     streamConn,
		recorder: recorder,
	}
}

func (c *fallbackReplayConn) CloseWrite() error {
	if c == nil {
		return nil
	}
	return connutil.TryCloseWrite(c.Conn)
}

func (c *fallbackReplayConn) CloseRead() error {
	if c == nil {
		return nil
	}
	return connutil.TryCloseRead(c.Conn)
}

func (c *fallbackReplayConn) GetBufferedAndRecorded() []byte {
	if c == nil || c.recorder == nil {
		return nil
	}
	return c.recorder.GetBufferedAndRecorded()
}

func readServerSession(conn net.Conn) (SessionKind, string, []byte, error) {
	for {
		msg, err := internaltunnel.ReadKIPMessage(conn)
		if err != nil {
			return SessionForward, "", nil, err
		}
		if msg.Type == internaltunnel.KIPTypeKeepAlive {
			continue
		}
		switch msg.Type {
		case internaltunnel.KIPTypeStartUoT:
			return SessionUoT, "", nil, nil
		case internaltunnel.KIPTypeStartMux:
			return SessionMux, "", nil, nil
		case internaltunnel.KIPTypeStartRev:
			return SessionReverse, "", msg.Payload, nil
		case internaltunnel.KIPTypeOpenTCP:
			targetAddr, err := readServerTarget(msg.Payload)
			if err != nil {
				return SessionForward, "", nil, err
			}
			return SessionForward, targetAddr, nil, nil
		default:
			return SessionForward, "", nil, fmt.Errorf("unknown session message: %d", msg.Type)
		}
	}
}

func readServerTarget(payload []byte) (string, error) {
	targetAddr, _, _, err := internalprotocol.ReadAddress(bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	return targetAddr, nil
}
