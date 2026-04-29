package sudoku

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	internalprotocol "github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
)

type HTTPMaskTunnelServer struct {
	cfg *ProtocolConfig
	ts  *httpmask.TunnelServer
}

func (s *HTTPMaskTunnelServer) Close() error { return nil }

func NewHTTPMaskTunnelServer(cfg *ProtocolConfig) *HTTPMaskTunnelServer {
	if cfg == nil {
		return &HTTPMaskTunnelServer{}
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
				}, cfg.tableCandidates(), nil),
			})
		}
	}

	return &HTTPMaskTunnelServer{cfg: cfg, ts: ts}
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
	if err := cfg.Validate(); err != nil {
		return nil, SessionForward, "", "", nil, err
	}

	conn, meta, err := internaltunnel.HandshakeAndUpgradeWithTablesMeta(rawConn, toInternalConfig(cfg), cfg.tableCandidates())
	if err != nil {
		return nil, SessionForward, "", "", nil, err
	}
	userHash := ""
	if meta != nil {
		userHash = meta.UserHash
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
		return handledServerHandshake(rawConn, s.cfg)
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
		return handledServerHandshakeWithFallbackConn(c, s.cfg, c, true)
	case httpmask.HandleStartTunnel:
		inner := *s.cfg
		inner.DisableHTTPMask = true
		return handledServerHandshakeWithFallbackConn(c, &inner, nil, false)
	default:
		return nil, SessionForward, "", "", nil, true, nil
	}
}

func handledServerHandshake(rawConn net.Conn, cfg *ProtocolConfig) (net.Conn, SessionKind, string, string, []byte, bool, error) {
	return handledServerHandshakeWithFallbackConn(rawConn, cfg, rawConn, true)
}

func handledServerHandshakeWithFallbackConn(rawConn net.Conn, cfg *ProtocolConfig, fallbackStream net.Conn, allowFallback bool) (net.Conn, SessionKind, string, string, []byte, bool, error) {
	conn, session, target, userHash, payload, err := ServerHandshakeSessionAutoWithUserHash(rawConn, cfg)
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
