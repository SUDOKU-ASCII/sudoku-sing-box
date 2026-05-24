package sudoku

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
	ctx          context.Context
	logger       log.ContextLogger
	dialer       N.Dialer
	baseConf     sudokut.ProtocolConfig
	httpMaskPool *sudokut.HTTPMaskTransportPool
	muxClient    *sudokut.MuxClient
	reverse      *reverseClient
}

type reverseClient struct {
	clientID string
	routes   []sudokut.ReverseRoute
	done     chan struct{}
	once     sync.Once
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
	paddingMin, paddingMax := resolvePadding(defaultConf.PaddingMin, defaultConf.PaddingMax, options.PaddingMin, options.PaddingMax)

	baseConf := sudokut.ProtocolConfig{
		ServerAddress:      net.JoinHostPort(options.Server, strconv.Itoa(int(options.ServerPort))),
		Key:                options.Key,
		AEADMethod:         defaultConf.AEADMethod,
		PaddingMin:         paddingMin,
		PaddingMax:         paddingMax,
		EnablePureDownlink: resolveBool(defaultConf.EnablePureDownlink, options.EnablePureDownlink),
		DisableHTTPMask:    options.DisableHTTPMask,
		HTTPMaskMode:       resolveHTTPMaskMode(defaultConf.HTTPMaskMode, options.HTTPMaskMode, options.HTTPMaskStrategy),
		HTTPMaskTLSEnabled: options.HTTPMaskTLS,
		HTTPMaskMultiplex:  resolveConfigString(defaultConf.HTTPMaskMultiplex, options.HTTPMaskMultiplex),
		HTTPMaskHost:       options.HTTPMaskHost,
		HTTPMaskPathRoot:   options.HTTPMaskPathRoot,
	}
	if options.AEADMethod != "" {
		baseConf.AEADMethod = options.AEADMethod
	}

	tables, err := buildProtocolTables(baseConf.Key, options.ASCII, options.CustomTable, options.CustomTables, true)
	if err != nil {
		return nil, E.Cause(err, "build table(s)")
	}
	applyProtocolTables(&baseConf, tables)

	out := &Outbound{
		Adapter:      outbound.NewAdapterWithDialerOptions(C.TypeSudoku, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		ctx:          ctx,
		logger:       logger,
		dialer:       outboundDialer,
		baseConf:     baseConf,
		httpMaskPool: new(sudokut.HTTPMaskTransportPool),
	}
	out.baseConf.DialContext = out.dialContext
	out.baseConf.HTTPMaskTransportPool = out.httpMaskPool

	if allowHTTPMaskMux(&out.baseConf) {
		out.muxClient, err = sudokut.NewMuxClient(&out.baseConf)
		if err != nil {
			return nil, err
		}
	}
	if len(options.Reverse.Routes) > 0 {
		routes := make([]sudokut.ReverseRoute, 0, len(options.Reverse.Routes))
		for _, route := range options.Reverse.Routes {
			routes = append(routes, sudokut.ReverseRoute{
				Path:        route.Path,
				Target:      route.Target,
				StripPrefix: route.StripPrefix,
				HostHeader:  route.HostHeader,
			})
		}
		out.reverse = &reverseClient{
			clientID: strings.TrimSpace(options.Reverse.ClientID),
			routes:   routes,
			done:     make(chan struct{}),
		}
	}

	return out, nil
}

func (h *Outbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart || h.reverse == nil {
		return nil
	}
	go h.serveReverse()
	return nil
}

func (h *Outbound) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return h.dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
}

func (h *Outbound) Close() error {
	if h.reverse != nil {
		h.reverse.once.Do(func() { close(h.reverse.done) })
	}
	return common.Close(common.PtrOrNil(h.muxClient), common.PtrOrNil(h.httpMaskPool))
}

func (h *Outbound) serveReverse() {
	backoff := 250 * time.Millisecond
	maxBackoff := 10 * time.Second
	for {
		select {
		case <-h.reverse.done:
			return
		default:
		}

		err := sudokut.DialReverseClientSession(h.ctx, &h.baseConf, h.reverse.clientID, h.reverse.routes)
		select {
		case <-h.reverse.done:
			return
		default:
		}
		if err != nil {
			h.logger.WarnContext(h.ctx, E.Cause(err, "Sudoku reverse session ended"))
		} else {
			h.logger.InfoContext(h.ctx, "Sudoku reverse session ended")
		}

		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-h.reverse.done:
			timer.Stop()
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
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
