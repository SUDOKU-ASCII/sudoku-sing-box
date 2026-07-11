package sudoku

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	sudokuc "github.com/sagernet/sing-box/transport/sudoku/crypto"
	internalconfig "github.com/sagernet/sing-box/transport/sudoku/internal/config"
	internalreverse "github.com/sagernet/sing-box/transport/sudoku/internal/reverse"
	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
)

type SessionKind int

const (
	SessionForward SessionKind = iota
	SessionUoT
	SessionMux
	SessionReverse
)

type ServerSession struct {
	Conn     net.Conn
	Type     SessionKind
	Target   string
	UserHash string
	Payload  []byte
}

type HTTPMaskTransportPool = httpmask.TransportPool
type SuspiciousError = internaltunnel.SuspiciousError

type ReverseRoute struct {
	Path        string
	Target      string
	StripPrefix *bool
	HostHeader  string
}

type ReverseManager struct {
	mgr    *internalreverse.Manager
	server *http.Server
	ln     net.Listener
	once   sync.Once
}

func toInternalConfig(cfg *ProtocolConfig) *internalconfig.Config {
	if cfg == nil {
		return nil
	}
	multiplex := cfg.MultiplexMode()
	return &internalconfig.Config{
		ServerAddress:      cfg.ServerAddress,
		Key:                cfg.Key,
		AEAD:               cfg.AEADMethod,
		PaddingMin:         cfg.PaddingMin,
		PaddingMax:         cfg.PaddingMax,
		EnablePureDownlink: cfg.EnablePureDownlink,
		Multiplex:          multiplex,
		SuspiciousAction:   cfg.SuspiciousAction,
		FallbackAddr:       cfg.FallbackAddress,
		HTTPMask: internalconfig.HTTPMaskConfig{
			Disable:   cfg.DisableHTTPMask,
			Mode:      strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)),
			TLS:       cfg.HTTPMaskTLSEnabled,
			Host:      cfg.HTTPMaskHost,
			PathRoot:  cfg.HTTPMaskPathRoot,
			Multiplex: multiplex,
		},
	}
}

func privateKeyBytes(key string) []byte {
	if _, err := sudokuc.RecoverPublicKey(key); err != nil {
		return nil
	}
	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		return nil
	}
	return keyBytes
}

func newBaseDialer(ctx context.Context, cfg *ProtocolConfig) *internaltunnel.BaseDialer {
	internalCfg := toInternalConfig(cfg)
	if internalCfg != nil {
		internalCfg.Key = clientTableSeed(cfg.Key)
	}
	return &internaltunnel.BaseDialer{
		Config:        internalCfg,
		Tables:        cfg.tableCandidates(),
		PrivateKey:    privateKeyBytes(cfg.Key),
		Context:       ctx,
		DialContext:   cfg.DialContext,
		TransportPool: cfg.HTTPMaskTransportPool,
	}
}

func convertReverseRoutes(routes []ReverseRoute) ([]internalconfig.ReverseRoute, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("no reverse routes")
	}

	converted := make([]internalconfig.ReverseRoute, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	seenTCP := false
	for i, r := range routes {
		r.Path = strings.TrimSpace(r.Path)
		r.Target = strings.TrimSpace(r.Target)
		r.HostHeader = strings.TrimSpace(r.HostHeader)

		if r.Path == "" && r.Target == "" {
			continue
		}
		if r.Target == "" {
			if r.Path == "" {
				return nil, fmt.Errorf("reverse tcp route[%d]: missing target", i)
			}
			return nil, fmt.Errorf("reverse route[%d] %q: missing target", i, r.Path)
		}
		if _, _, err := net.SplitHostPort(r.Target); err != nil {
			if r.Path == "" {
				return nil, fmt.Errorf("reverse tcp route[%d]: invalid target %q: %w", i, r.Target, err)
			}
			return nil, fmt.Errorf("reverse route[%d] %q: invalid target %q: %w", i, r.Path, r.Target, err)
		}

		if r.Path == "" {
			if seenTCP {
				return nil, fmt.Errorf("reverse route duplicate tcp mapping")
			}
			seenTCP = true
			converted = append(converted, internalconfig.ReverseRoute{
				Path:        "",
				Target:      r.Target,
				StripPrefix: r.StripPrefix,
				HostHeader:  r.HostHeader,
			})
			continue
		}

		if !strings.HasPrefix(r.Path, "/") {
			r.Path = "/" + r.Path
		}
		r.Path = path.Clean(r.Path)
		if r.Path != "/" {
			r.Path = strings.TrimRight(r.Path, "/")
		}

		if _, ok := seen[r.Path]; ok {
			return nil, fmt.Errorf("reverse route duplicate path: %q", r.Path)
		}
		seen[r.Path] = struct{}{}

		converted = append(converted, internalconfig.ReverseRoute{
			Path:        r.Path,
			Target:      r.Target,
			StripPrefix: r.StripPrefix,
			HostHeader:  r.HostHeader,
		})
	}
	if len(converted) == 0 {
		return nil, fmt.Errorf("no usable reverse routes")
	}
	return converted, nil
}

func NewReverseManager(listenAddr string) (*ReverseManager, error) {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return nil, fmt.Errorf("missing reverse listen address")
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	mgr := internalreverse.NewManager()
	rm := &ReverseManager{
		mgr: mgr,
		ln:  ln,
		server: &http.Server{
			Handler:           mgr,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
	return rm, nil
}

func (m *ReverseManager) Serve() error {
	if m == nil || m.server == nil || m.ln == nil {
		return fmt.Errorf("reverse manager is not initialized")
	}

	httpLn := newReverseConnChanListener(m.ln.Addr(), 256)
	httpErrCh := make(chan error, 1)
	go func() { httpErrCh <- m.server.Serve(httpLn) }()
	defer func() {
		_ = httpLn.Close()
		_ = m.server.Close()
		<-httpErrCh
	}()

	for {
		c, err := m.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		go func(raw net.Conn) {
			if raw == nil {
				return
			}

			var peek [4]byte
			_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := io.ReadFull(raw, peek[:])
			_ = raw.SetReadDeadline(time.Time{})
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					if n > 0 {
						m.mgr.ServeTCP(internaltunnel.NewPreBufferedConn(raw, peek[:n]))
						return
					}
					m.mgr.ServeTCP(raw)
					return
				}
				_ = raw.Close()
				return
			}

			conn := internaltunnel.NewPreBufferedConn(raw, peek[:n])
			if httpmask.LooksLikeHTTPRequestStart(peek[:]) {
				if !httpLn.enqueue(conn) {
					_ = conn.Close()
				}
				return
			}
			m.mgr.ServeTCP(conn)
		}(c)
	}
}

func (m *ReverseManager) Close() error {
	if m == nil {
		return nil
	}
	var err error
	m.once.Do(func() {
		if m.server != nil {
			err = m.server.Close()
		}
		if m.ln != nil {
			if lnErr := m.ln.Close(); err == nil {
				err = lnErr
			}
		}
	})
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (m *ReverseManager) HandleServerSession(conn net.Conn, userHash string, helloPayload []byte) error {
	if m == nil || m.mgr == nil {
		return fmt.Errorf("reverse manager is not configured")
	}
	return internalreverse.HandleServerSession(conn, userHash, m.mgr, helloPayload)
}

type reverseConnChanListener struct {
	addr   net.Addr
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newReverseConnChanListener(addr net.Addr, buffer int) *reverseConnChanListener {
	if buffer <= 0 {
		buffer = 1
	}
	return &reverseConnChanListener{
		addr:   addr,
		ch:     make(chan net.Conn, buffer),
		closed: make(chan struct{}),
	}
}

func (l *reverseConnChanListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case c := <-l.ch:
		return c, nil
	}
}

func (l *reverseConnChanListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *reverseConnChanListener) Addr() net.Addr { return l.addr }

func (l *reverseConnChanListener) enqueue(c net.Conn) bool {
	select {
	case <-l.closed:
		return false
	default:
	}
	select {
	case l.ch <- c:
		return true
	case <-l.closed:
		return false
	}
}

func DialReverseClientSession(ctx context.Context, cfg *ProtocolConfig, clientID string, routes []ReverseRoute) error {
	converted, err := convertReverseRoutes(routes)
	if err != nil {
		return err
	}
	dialer, err := newStandardDialer(ctx, cfg, (*ProtocolConfig).Validate)
	if err != nil {
		return err
	}
	conn, err := dialer.BaseDialer.DialReverseBase()
	if err != nil {
		return err
	}
	if ctx != nil {
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-done:
			}
		}()
		defer close(done)
	}
	err = internalreverse.ServeClientSession(conn, clientID, converted)
	_ = conn.Close()
	return err
}
