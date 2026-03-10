package sudoku

import (
	"bytes"
	"fmt"
	"net"
	"strings"

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
				Mode:     cfg.HTTPMaskMode,
				PathRoot: cfg.HTTPMaskPathRoot,
				AuthKey:  cfg.Key,
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

	for {
		msg, err := internaltunnel.ReadKIPMessage(conn)
		if err != nil {
			_ = conn.Close()
			return nil, SessionForward, "", "", nil, err
		}
		if msg.Type == internaltunnel.KIPTypeKeepAlive {
			continue
		}
		switch msg.Type {
		case internaltunnel.KIPTypeStartUoT:
			return conn, SessionUoT, "", userHash, nil, nil
		case internaltunnel.KIPTypeStartMux:
			return conn, SessionMux, "", userHash, nil, nil
		case internaltunnel.KIPTypeStartRev:
			return conn, SessionReverse, "", userHash, msg.Payload, nil
		case internaltunnel.KIPTypeOpenTCP:
			targetAddr, _, _, err := internalprotocol.ReadAddress(bytes.NewReader(msg.Payload))
			if err != nil {
				_ = conn.Close()
				return nil, SessionForward, "", "", nil, err
			}
			return conn, SessionForward, targetAddr, userHash, nil, nil
		default:
			_ = conn.Close()
			return nil, SessionForward, "", "", nil, fmt.Errorf("unknown session message: %d", msg.Type)
		}
	}
}

func (s *HTTPMaskTunnelServer) HandleConnSessionAutoWithUserHash(rawConn net.Conn) (net.Conn, SessionKind, string, string, []byte, bool, error) {
	if s == nil || s.cfg == nil {
		return nil, SessionForward, "", "", nil, false, nil
	}
	if s.ts == nil {
		conn, session, target, userHash, payload, err := ServerHandshakeSessionAutoWithUserHash(rawConn, s.cfg)
		return conn, session, target, userHash, payload, true, err
	}

	res, c, err := s.ts.HandleConn(rawConn)
	if err != nil {
		return nil, SessionForward, "", "", nil, true, err
	}

	switch res {
	case httpmask.HandleDone:
		return nil, SessionForward, "", "", nil, true, nil
	case httpmask.HandlePassThrough:
		conn, session, target, userHash, payload, err := ServerHandshakeSessionAutoWithUserHash(c, s.cfg)
		return conn, session, target, userHash, payload, true, err
	case httpmask.HandleStartTunnel:
		inner := *s.cfg
		inner.DisableHTTPMask = true
		conn, session, target, userHash, payload, err := ServerHandshakeSessionAutoWithUserHash(c, &inner)
		return conn, session, target, userHash, payload, true, err
	default:
		return nil, SessionForward, "", "", nil, true, nil
	}
}
