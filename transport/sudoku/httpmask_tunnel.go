package sudoku

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
)

type HTTPMaskTunnelServer struct {
	cfg *ProtocolConfig
	ts  *httpmask.TunnelServer
}

func NewHTTPMaskTunnelServer(cfg *ProtocolConfig) *HTTPMaskTunnelServer {
	if cfg == nil {
		return &HTTPMaskTunnelServer{}
	}

	var ts *httpmask.TunnelServer
	if !cfg.DisableHTTPMask {
		switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
		case "stream", "poll", "auto":
			passThroughOnReject := false
			switch strings.ToLower(strings.TrimSpace(cfg.SuspiciousAction)) {
			case "silent":
				passThroughOnReject = true
			case "", "fallback":
				passThroughOnReject = strings.TrimSpace(cfg.FallbackAddress) != ""
			}
			ts = httpmask.NewTunnelServer(httpmask.TunnelServerOptions{
				Mode:                cfg.HTTPMaskMode,
				PathRoot:            cfg.HTTPMaskPathRoot,
				AuthKey:             cfg.Key,
				PassThroughOnReject: passThroughOnReject,
			})
		}
	}
	return &HTTPMaskTunnelServer{cfg: cfg, ts: ts}
}

func (s *HTTPMaskTunnelServer) Close() error {
	if s == nil || s.ts == nil {
		return nil
	}
	return s.ts.Close()
}

// WrapConn inspects an accepted TCP connection and upgrades it to an HTTP tunnel stream when needed.
//
// Returns:
//   - done=true: this TCP connection has been fully handled (e.g., stream/poll control request), caller should return
//   - done=false: handshakeConn+cfg are ready for ServerHandshake
func (s *HTTPMaskTunnelServer) WrapConn(rawConn net.Conn) (handshakeConn net.Conn, cfg *ProtocolConfig, allowFallback bool, done bool, err error) {
	if rawConn == nil {
		return nil, nil, false, true, fmt.Errorf("nil conn")
	}
	if s == nil {
		return rawConn, nil, true, false, nil
	}
	if s.ts == nil {
		return rawConn, s.cfg, true, false, nil
	}

	res, c, err := s.ts.HandleConn(rawConn)
	if err != nil {
		return nil, nil, false, true, err
	}

	switch res {
	case httpmask.HandleDone:
		return nil, nil, false, true, nil
	case httpmask.HandlePassThrough:
		return c, s.cfg, true, false, nil
	case httpmask.HandleStartTunnel:
		inner := *s.cfg
		inner.DisableHTTPMask = true
		return c, &inner, false, false, nil
	default:
		return nil, nil, false, true, nil
	}
}

type TunnelDialer func(ctx context.Context, network, addr string) (net.Conn, error)

type HTTPMaskTransportPool = httpmask.TransportPool

type HTTPMaskTunnelDialOptions struct {
	Dial          TunnelDialer
	TransportPool *HTTPMaskTransportPool
}

// DialHTTPMaskTunnel dials a CDN-capable HTTP tunnel (stream/poll/auto) and returns a stream carrying raw Sudoku bytes.
func DialHTTPMaskTunnel(ctx context.Context, serverAddress string, cfg *ProtocolConfig, opts HTTPMaskTunnelDialOptions) (net.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if opts.Dial == nil {
		return nil, fmt.Errorf("dialer is required")
	}
	if cfg.DisableHTTPMask {
		return nil, fmt.Errorf("http mask is disabled")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
	case "stream", "poll", "auto":
	default:
		return nil, fmt.Errorf("http_mask_mode=%q does not use http tunnel", cfg.HTTPMaskMode)
	}
	return httpmask.DialTunnel(ctx, serverAddress, httpmask.TunnelDialOptions{
		Mode:          cfg.HTTPMaskMode,
		TLSEnabled:    cfg.HTTPMaskTLSEnabled,
		HostOverride:  cfg.HTTPMaskHost,
		PathRoot:      cfg.HTTPMaskPathRoot,
		AuthKey:       ClientAEADSeed(cfg.Key),
		Multiplex:     cfg.HTTPMaskMultiplex,
		TransportPool: opts.TransportPool,
		DialContext:   opts.Dial,
	})
}
