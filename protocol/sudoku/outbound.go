package sudoku

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sudokut "github.com/sagernet/sing-box/transport/sudoku"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.SudokuOutboundOptions](registry, C.TypeSudoku, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	ctx              context.Context
	dialer           N.Dialer
	server           M.Socksaddr
	baseConf         sudokut.ProtocolConfig
	httpMaskPool     *sudokut.HTTPMaskTransportPool
	muxClient        *sudokut.MuxClient
	httpMaskStrategy string
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SudokuOutboundOptions) (adapter.Outbound, error) {
	if options.Server == "" {
		return nil, E.New("missing server")
	}
	if options.ServerPort == 0 {
		return nil, E.New("missing server_port")
	}
	if options.Key == "" {
		return nil, E.New("missing key")
	}

	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}

	defaultConf := sudokut.DefaultConfig()

	tableType := resolveTableType(options.ASCII)
	paddingMin, paddingMax := resolvePadding(defaultConf.PaddingMin, defaultConf.PaddingMax, options.PaddingMin, options.PaddingMax)
	enablePureDownlink := resolveBool(defaultConf.EnablePureDownlink, options.EnablePureDownlink)

	serverAddress := net.JoinHostPort(options.Server, strconv.Itoa(int(options.ServerPort)))
	baseConf := sudokut.ProtocolConfig{
		ServerAddress:           serverAddress,
		Key:                     options.Key,
		AEADMethod:              defaultConf.AEADMethod,
		PaddingMin:              paddingMin,
		PaddingMax:              paddingMax,
		EnablePureDownlink:      enablePureDownlink,
		HandshakeTimeoutSeconds: defaultConf.HandshakeTimeoutSeconds,
		DisableHTTPMask:         options.DisableHTTPMask,
		HTTPMaskMode:            defaultConf.HTTPMaskMode,
		HTTPMaskTLSEnabled:      options.HTTPMaskTLS,
		HTTPMaskMultiplex:       defaultConf.HTTPMaskMultiplex,
		HTTPMaskHost:            options.HTTPMaskHost,
		HTTPMaskPathRoot:        options.HTTPMaskPathRoot,
	}
	if options.AEADMethod != "" {
		baseConf.AEADMethod = options.AEADMethod
	}
	if options.HTTPMaskMode != "" {
		baseConf.HTTPMaskMode = options.HTTPMaskMode
	}
	if options.HTTPMaskMultiplex != "" {
		baseConf.HTTPMaskMultiplex = options.HTTPMaskMultiplex
	}

	tables, err := sudokut.NewTablesWithCustomPatterns(sudokut.ClientAEADSeed(options.Key), tableType, options.CustomTable, options.CustomTables)
	if err != nil {
		return nil, E.Cause(err, "build table(s)")
	}
	if len(tables) == 1 {
		baseConf.Table = tables[0]
	} else {
		baseConf.Tables = tables
	}

	out := &Outbound{
		Adapter:          outbound.NewAdapterWithDialerOptions(C.TypeSudoku, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		ctx:              ctx,
		dialer:           outboundDialer,
		server:           options.ServerOptions.Build(),
		baseConf:         baseConf,
		httpMaskPool:     new(sudokut.HTTPMaskTransportPool),
		httpMaskStrategy: options.HTTPMaskStrategy,
	}

	httpMaskMode := strings.ToLower(strings.TrimSpace(out.baseConf.HTTPMaskMode))
	httpMaskMux := strings.ToLower(strings.TrimSpace(out.baseConf.HTTPMaskMultiplex))
	if !out.baseConf.DisableHTTPMask && (httpMaskMode == "stream" || httpMaskMode == "poll" || httpMaskMode == "auto") && httpMaskMux == "on" {
		out.muxClient = sudokut.NewMuxClient(out.dialBaseForMux)
	}

	return out, nil
}

func (h *Outbound) Close() error {
	return common.Close(common.PtrOrNil(h.muxClient), common.PtrOrNil(h.httpMaskPool))
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination

	switch N.NetworkName(network) {
	case N.NetworkTCP:
		metadata.Network = N.NetworkTCP
		return h.dialTCP(ctx, destination)
	case N.NetworkUDP:
		return nil, E.New("UDP dial is not supported, use listen_packet instead")
	default:
		return nil, os.ErrInvalid
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	metadata.Network = N.NetworkUDP
	return h.dialUoT(ctx)
}

func (h *Outbound) dialTCP(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	if h.muxClient != nil {
		return h.muxClient.Dial(ctx, destination.String())
	}

	cfg := h.baseConf
	cfg.TargetAddress = destination.String()
	if err := cfg.ValidateClient(); err != nil {
		return nil, err
	}

	dialFn := func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		return h.dialer.DialContext(dialCtx, network, M.ParseSocksaddr(addr))
	}

	var (
		rawConn net.Conn
		err     error
	)
	if !cfg.DisableHTTPMask {
		switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
		case "stream", "poll", "auto":
			rawConn, err = sudokut.DialHTTPMaskTunnel(ctx, cfg.ServerAddress, &cfg, sudokut.HTTPMaskTunnelDialOptions{
				Dial:          dialFn,
				TransportPool: h.httpMaskPool,
			})
		}
	}
	if rawConn == nil && err == nil {
		rawConn, err = h.dialer.DialContext(ctx, N.NetworkTCP, h.server)
	}
	if err != nil {
		return nil, err
	}

	success := false
	defer func() {
		if !success {
			common.Close(rawConn)
		}
	}()

	handshakeCfg := cfg
	if !handshakeCfg.DisableHTTPMask {
		switch strings.ToLower(strings.TrimSpace(handshakeCfg.HTTPMaskMode)) {
		case "stream", "poll", "auto":
			handshakeCfg.DisableHTTPMask = true
		}
	}
	c, err := sudokut.ClientHandshakeWithOptions(rawConn, &handshakeCfg, sudokut.ClientHandshakeOptions{HTTPMaskStrategy: h.httpMaskStrategy})
	if err != nil {
		return nil, err
	}

	addrBuf, err := sudokut.EncodeAddress(cfg.TargetAddress)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("encode target address failed: %w", err)
	}
	if _, err := c.Write(addrBuf); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("send target address failed: %w", err)
	}

	success = true
	return c, nil
}

func (h *Outbound) dialUoT(ctx context.Context) (net.PacketConn, error) {
	cfg := h.baseConf
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	dialFn := func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		return h.dialer.DialContext(dialCtx, network, M.ParseSocksaddr(addr))
	}

	var (
		rawConn net.Conn
		err     error
	)
	if !cfg.DisableHTTPMask {
		switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
		case "stream", "poll", "auto":
			rawConn, err = sudokut.DialHTTPMaskTunnel(ctx, cfg.ServerAddress, &cfg, sudokut.HTTPMaskTunnelDialOptions{
				Dial:          dialFn,
				TransportPool: h.httpMaskPool,
			})
		}
	}
	if rawConn == nil && err == nil {
		rawConn, err = h.dialer.DialContext(ctx, N.NetworkTCP, h.server)
	}
	if err != nil {
		return nil, err
	}

	success := false
	defer func() {
		if !success {
			common.Close(rawConn)
		}
	}()

	handshakeCfg := cfg
	if !handshakeCfg.DisableHTTPMask {
		switch strings.ToLower(strings.TrimSpace(handshakeCfg.HTTPMaskMode)) {
		case "stream", "poll", "auto":
			handshakeCfg.DisableHTTPMask = true
		}
	}
	c, err := sudokut.ClientHandshakeWithOptions(rawConn, &handshakeCfg, sudokut.ClientHandshakeOptions{HTTPMaskStrategy: h.httpMaskStrategy})
	if err != nil {
		return nil, err
	}

	if err := sudokut.WritePreface(c); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("send uot preface failed: %w", err)
	}

	success = true
	return sudokut.NewUoTPacketConn(c), nil
}

func (h *Outbound) dialBaseForMux(ctx context.Context) (net.Conn, error) {
	cfg := h.baseConf
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.DisableHTTPMask {
		return nil, E.New("mux requires disable_http_mask=false")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
	case "stream", "poll", "auto":
	default:
		return nil, E.New("mux requires http_mask_mode=stream/poll/auto (got ", cfg.HTTPMaskMode, ")")
	}

	dialFn := func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		return h.dialer.DialContext(dialCtx, network, M.ParseSocksaddr(addr))
	}

	rawConn, err := sudokut.DialHTTPMaskTunnel(ctx, cfg.ServerAddress, &cfg, sudokut.HTTPMaskTunnelDialOptions{
		Dial:          dialFn,
		TransportPool: h.httpMaskPool,
	})
	if err != nil {
		return nil, err
	}

	success := false
	defer func() {
		if !success {
			common.Close(rawConn)
		}
	}()

	handshakeCfg := cfg
	handshakeCfg.DisableHTTPMask = true
	c, err := sudokut.ClientHandshakeWithOptions(rawConn, &handshakeCfg, sudokut.ClientHandshakeOptions{HTTPMaskStrategy: h.httpMaskStrategy})
	if err != nil {
		return nil, err
	}

	success = true
	return c, nil
}
