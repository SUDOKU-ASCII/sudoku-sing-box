package sudoku

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sudokut "github.com/sagernet/sing-box/transport/sudoku"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/sagernet/sing/common/bufio"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.SudokuInboundOptions](registry, C.TypeSudoku, NewInbound)
}

var _ adapter.TCPInjectableInbound = (*Inbound)(nil)

type Inbound struct {
	inbound.Adapter
	ctx       context.Context
	router    adapter.ConnectionRouterEx
	logger    logger.ContextLogger
	listener  *listener.Listener
	protoConf sudokut.ProtocolConfig
	tunnelSrv *sudokut.HTTPMaskTunnelServer
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SudokuInboundOptions) (adapter.Inbound, error) {
	if options.Key == "" {
		return nil, E.New("missing key")
	}

	defaultConf := sudokut.DefaultConfig()

	tableType := resolveTableType(options.ASCII)
	paddingMin, paddingMax := resolvePadding(defaultConf.PaddingMin, defaultConf.PaddingMax, options.PaddingMin, options.PaddingMax)
	enablePureDownlink := resolveBool(defaultConf.EnablePureDownlink, options.EnablePureDownlink)

	handshakeTimeout := defaultConf.HandshakeTimeoutSeconds
	if options.HandshakeTimeout > 0 {
		handshakeTimeout = options.HandshakeTimeout
	}

	protoConf := sudokut.ProtocolConfig{
		Key:                     options.Key,
		AEADMethod:              defaultConf.AEADMethod,
		PaddingMin:              paddingMin,
		PaddingMax:              paddingMax,
		EnablePureDownlink:      enablePureDownlink,
		HandshakeTimeoutSeconds: handshakeTimeout,
		SuspiciousAction:        defaultConf.SuspiciousAction,
		FallbackAddress:         options.FallbackAddress,
		DisableHTTPMask:         options.DisableHTTPMask,
		HTTPMaskMode:            defaultConf.HTTPMaskMode,
		HTTPMaskMultiplex:       defaultConf.HTTPMaskMultiplex,
		HTTPMaskPathRoot:        options.HTTPMaskPathRoot,
	}
	if options.AEADMethod != "" {
		protoConf.AEADMethod = options.AEADMethod
	}
	if options.SuspiciousAction != "" {
		protoConf.SuspiciousAction = options.SuspiciousAction
	}
	if options.HTTPMaskMode != "" {
		protoConf.HTTPMaskMode = options.HTTPMaskMode
	}
	if options.HTTPMaskMultiplex != "" {
		protoConf.HTTPMaskMultiplex = options.HTTPMaskMultiplex
	}

	tables, err := sudokut.NewTablesWithCustomPatterns(protoConf.Key, tableType, options.CustomTable, options.CustomTables)
	if err != nil {
		return nil, E.Cause(err, "build table(s)")
	}
	if len(tables) == 1 {
		protoConf.Table = tables[0]
	} else {
		protoConf.Tables = tables
	}

	in := &Inbound{
		Adapter:   inbound.NewAdapter(C.TypeSudoku, tag),
		ctx:       ctx,
		router:    router,
		logger:    logger,
		protoConf: protoConf,
	}
	in.tunnelSrv = sudokut.NewHTTPMaskTunnelServer(&in.protoConf)
	in.listener = listener.New(listener.Options{
		Context:           ctx,
		Logger:            logger,
		Network:           []string{N.NetworkTCP},
		Listen:            options.ListenOptions,
		ConnectionHandler: in,
	})
	return in, nil
}

func (h *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	return h.listener.Start()
}

func (h *Inbound) Close() error {
	return common.Close(
		h.listener,
		common.PtrOrNil(h.tunnelSrv),
	)
}

func (h *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	handshakeConn := conn
	handshakeCfg := &h.protoConf
	allowFallback := true
	if h.tunnelSrv != nil {
		c, cfg, allow, done, err := h.tunnelSrv.WrapConn(conn)
		if err != nil {
			N.CloseOnHandshakeFailure(conn, onClose, err)
			h.logger.ErrorContext(ctx, E.Cause(err, "wrap http tunnel from ", metadata.Source))
			return
		}
		if done {
			return
		}
		allowFallback = allow
		if c != nil {
			handshakeConn = c
		}
		if cfg != nil {
			handshakeCfg = cfg
		}
	}

	if allowFallback {
		if r, ok := handshakeConn.(interface{ IsHTTPMaskRejected() bool }); ok && r.IsHTTPMaskRejected() {
			h.handleSuspicious(ctx, handshakeConn, conn, handshakeCfg, metadata.Source, onClose)
			return
		}
	}

	session, err := sudokut.ServerHandshake(handshakeConn, handshakeCfg)
	if err != nil {
		var suspErr *sudokut.SuspiciousError
		if allowFallback && errors.As(err, &suspErr) {
			h.handleSuspicious(ctx, suspErr.Conn, conn, handshakeCfg, metadata.Source, onClose)
			return
		}
		N.CloseOnHandshakeFailure(handshakeConn, onClose, err)
		if handshakeConn != conn {
			common.Close(conn)
		}
		h.logger.ErrorContext(ctx, E.Cause(err, "process connection from ", metadata.Source))
		return
	}

	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()

	switch session.Type {
	case sudokut.SessionTypeUoT:
		h.logger.InfoContext(ctx, "inbound Sudoku UoT session from ", metadata.Source)
		metadata.Destination = M.Socksaddr{}
		packetConn := bufio.NewPacketConn(sudokut.NewUoTPacketConn(session.Conn))
		h.router.RoutePacketConnectionEx(ctx, packetConn, metadata, onClose)
	case sudokut.SessionTypeMux:
		h.logger.InfoContext(ctx, "inbound Sudoku mux session from ", metadata.Source)
		err = sudokut.HandleMuxServer(session.Conn, func(stream net.Conn, targetAddr string) {
			streamCtx := log.ContextWithNewID(ctx)
			target := M.ParseSocksaddr(targetAddr)
			if !target.IsValid() {
				_ = stream.Close()
				return
			}
			streamMetadata := metadata
			streamMetadata.Destination = target
			h.logger.InfoContext(streamCtx, "inbound connection to ", streamMetadata.Destination)
			h.router.RouteConnectionEx(streamCtx, stream, streamMetadata, nil)
		})
		if err != nil {
			h.logger.ErrorContext(ctx, E.Cause(err, "process mux session from ", metadata.Source))
		}
	default:
		target := M.ParseSocksaddr(session.Target)
		if !target.IsValid() {
			N.CloseOnHandshakeFailure(session.Conn, onClose, E.New("invalid target: ", session.Target))
			return
		}
		metadata.Destination = target
		h.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
		h.router.RouteConnectionEx(ctx, session.Conn, metadata, onClose)
	}
}

func (h *Inbound) handleSuspicious(ctx context.Context, wrapper net.Conn, rawConn net.Conn, cfg *sudokut.ProtocolConfig, source M.Socksaddr, onClose N.CloseHandlerFunc) {
	defer func() {
		if onClose != nil {
			onClose(nil)
		}
	}()

	action := strings.ToLower(strings.TrimSpace(cfg.SuspiciousAction))
	if action == "" {
		action = "fallback"
	}

	switch action {
	case "silent":
		h.logger.WarnContext(ctx, "suspicious connection from ", source, ", silent")
		_ = rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.Copy(io.Discard, rawConn)
		time.Sleep(5 * time.Second)
		common.Close(rawConn)
		return

	case "fallback":
		fallbackAddr := strings.TrimSpace(cfg.FallbackAddress)
		if fallbackAddr == "" {
			h.logger.WarnContext(ctx, "suspicious connection from ", source, ", no fallback_address")
			common.Close(rawConn)
			return
		}

		h.logger.InfoContext(ctx, "fallback suspicious connection from ", source, " -> ", fallbackAddr)
		dst, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, N.NetworkTCP, fallbackAddr)
		if err != nil {
			h.logger.ErrorContext(ctx, E.Cause(err, "dial fallback"))
			common.Close(rawConn)
			return
		}

		var badData []byte
		if recorder, ok := wrapper.(interface{ GetBufferedAndRecorded() []byte }); ok {
			badData = recorder.GetBufferedAndRecorded()
		}
		if len(badData) > 0 {
			_ = dst.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err := writeFullConn(dst, badData); err != nil {
				h.logger.ErrorContext(ctx, E.Cause(err, "write fallback prelude"))
				common.Close(dst, rawConn)
				return
			}
			_ = dst.SetWriteDeadline(time.Time{})
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			defer common.Close(dst)
			_, _ = io.Copy(dst, rawConn)
		}()
		go func() {
			defer wg.Done()
			defer common.Close(rawConn)
			_, _ = io.Copy(rawConn, dst)
		}()
		wg.Wait()
		return

	default:
		h.logger.WarnContext(ctx, "suspicious connection from ", source, ", unknown action: ", cfg.SuspiciousAction)
		common.Close(rawConn)
		return
	}
}

func writeFullConn(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
