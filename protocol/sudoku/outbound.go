package sudoku

import (
	"context"
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
	dialer       N.Dialer
	baseConf     sudokut.ProtocolConfig
	httpMaskPool *sudokut.HTTPMaskTransportPool
	muxClient    *sudokut.MuxClient
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

	httpMaskMode := defaultConf.HTTPMaskMode
	if options.HTTPMaskMode != "" {
		httpMaskMode = options.HTTPMaskMode
	} else if strings.EqualFold(strings.TrimSpace(options.HTTPMaskStrategy), "websocket") {
		httpMaskMode = "ws"
	}

	baseConf := sudokut.ProtocolConfig{
		ServerAddress:      net.JoinHostPort(options.Server, strconv.Itoa(int(options.ServerPort))),
		Key:                options.Key,
		AEADMethod:         defaultConf.AEADMethod,
		PaddingMin:         paddingMin,
		PaddingMax:         paddingMax,
		EnablePureDownlink: enablePureDownlink,
		DisableHTTPMask:    options.DisableHTTPMask,
		HTTPMaskMode:       httpMaskMode,
		HTTPMaskTLSEnabled: options.HTTPMaskTLS,
		HTTPMaskMultiplex:  defaultConf.HTTPMaskMultiplex,
		HTTPMaskHost:       options.HTTPMaskHost,
		HTTPMaskPathRoot:   options.HTTPMaskPathRoot,
	}
	if options.AEADMethod != "" {
		baseConf.AEADMethod = options.AEADMethod
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
		Adapter:      outbound.NewAdapterWithDialerOptions(C.TypeSudoku, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		dialer:       outboundDialer,
		baseConf:     baseConf,
		httpMaskPool: new(sudokut.HTTPMaskTransportPool),
	}
	out.baseConf.DialContext = out.dialContext
	out.baseConf.HTTPMaskTransportPool = out.httpMaskPool

	mode := strings.ToLower(strings.TrimSpace(out.baseConf.HTTPMaskMode))
	muxMode := strings.ToLower(strings.TrimSpace(out.baseConf.HTTPMaskMultiplex))
	if !out.baseConf.DisableHTTPMask && (mode == "stream" || mode == "poll" || mode == "auto" || mode == "ws") && muxMode == "on" {
		out.muxClient, err = sudokut.NewMuxClient(&out.baseConf)
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

func (h *Outbound) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return h.dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
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
		if h.muxClient != nil {
			return h.muxClient.Dial(ctx, destination.String())
		}
		cfg := h.baseConf
		cfg.TargetAddress = destination.String()
		return sudokut.Dial(ctx, &cfg)
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

	cfg := h.baseConf
	conn, err := sudokut.DialUDPOverTCP(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	return sudokut.NewUoTPacketConn(conn), nil
}
