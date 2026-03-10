package sudoku

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
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

	httpMaskMode := defaultConf.HTTPMaskMode
	if options.HTTPMaskMode != "" {
		httpMaskMode = options.HTTPMaskMode
	}

	protoConf := sudokut.ProtocolConfig{
		Key:                     options.Key,
		AEADMethod:              defaultConf.AEADMethod,
		PaddingMin:              paddingMin,
		PaddingMax:              paddingMax,
		EnablePureDownlink:      enablePureDownlink,
		HandshakeTimeoutSeconds: defaultConf.HandshakeTimeoutSeconds,
		SuspiciousAction:        defaultConf.SuspiciousAction,
		FallbackAddress:         options.FallbackAddress,
		DisableHTTPMask:         options.DisableHTTPMask,
		HTTPMaskMode:            httpMaskMode,
		HTTPMaskMultiplex:       defaultConf.HTTPMaskMultiplex,
		HTTPMaskPathRoot:        options.HTTPMaskPathRoot,
	}
	if options.AEADMethod != "" {
		protoConf.AEADMethod = options.AEADMethod
	}
	if options.HandshakeTimeout > 0 {
		protoConf.HandshakeTimeoutSeconds = options.HandshakeTimeout
	}
	if options.SuspiciousAction != "" {
		protoConf.SuspiciousAction = options.SuspiciousAction
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
		tunnelSrv: sudokut.NewHTTPMaskTunnelServer(&protoConf),
	}
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
	return common.Close(h.listener, common.PtrOrNil(h.tunnelSrv))
}

func (h *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	sessionConn, session, targetAddr, userHash, payload, handled, err := h.tunnelSrv.HandleConnSessionAutoWithUserHash(conn)
	if err != nil {
		var suspErr *sudokut.SuspiciousError
		if errors.As(err, &suspErr) {
			h.handleSuspicious(ctx, suspErr, &h.protoConf, metadata.Source, onClose)
			return
		}
		N.CloseOnHandshakeFailure(conn, onClose, err)
		h.logger.ErrorContext(ctx, E.Cause(err, "process connection from ", metadata.Source))
		return
	}
	if !handled || sessionConn == nil {
		return
	}

	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()

	if userHash != "" {
		h.logger.DebugContext(ctx, "sudoku user=", userHash)
	}

	switch session {
	case sudokut.SessionUoT:
		h.logger.InfoContext(ctx, "inbound Sudoku UoT session from ", metadata.Source)
		metadata.Destination = M.Socksaddr{}
		packetConn := bufio.NewPacketConn(sudokut.NewUoTPacketConn(sessionConn))
		h.router.RoutePacketConnectionEx(ctx, packetConn, metadata, onClose)
	case sudokut.SessionMux:
		h.logger.InfoContext(ctx, "inbound Sudoku mux session from ", metadata.Source)
		err = sudokut.HandleMuxServer(sessionConn, func(stream net.Conn, target string) {
			streamCtx := log.ContextWithNewID(ctx)
			targetAddr := M.ParseSocksaddr(target)
			if !targetAddr.IsValid() {
				_ = stream.Close()
				return
			}
			streamMetadata := metadata
			streamMetadata.Destination = targetAddr
			h.logger.InfoContext(streamCtx, "inbound connection to ", streamMetadata.Destination)
			h.router.RouteConnectionEx(streamCtx, stream, streamMetadata, nil)
		})
		if err != nil {
			h.logger.ErrorContext(ctx, E.Cause(err, "process mux session from ", metadata.Source))
		}
	case sudokut.SessionForward:
		target := M.ParseSocksaddr(targetAddr)
		if !target.IsValid() {
			N.CloseOnHandshakeFailure(sessionConn, onClose, E.New("invalid target: ", targetAddr))
			return
		}
		metadata.Destination = target
		h.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
		h.router.RouteConnectionEx(ctx, sessionConn, metadata, onClose)
	default:
		_ = sessionConn.Close()
		h.logger.WarnContext(ctx, "unsupported Sudoku session from ", metadata.Source, ", payload=", len(payload))
	}
}

func (h *Inbound) handleSuspicious(ctx context.Context, suspErr *sudokut.SuspiciousError, cfg *sudokut.ProtocolConfig, source M.Socksaddr, onClose N.CloseHandlerFunc) {
	defer func() {
		if onClose != nil {
			onClose(nil)
		}
	}()

	action := strings.ToLower(strings.TrimSpace(cfg.SuspiciousAction))
	if action == "" {
		action = "fallback"
	}

	rawConn := suspErr.Conn
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

		if recorder, ok := rawConn.(interface{ GetBufferedAndRecorded() []byte }); ok {
			if badData := recorder.GetBufferedAndRecorded(); len(badData) > 0 {
				_ = dst.SetWriteDeadline(time.Now().Add(3 * time.Second))
				if _, err := dst.Write(badData); err != nil {
					h.logger.ErrorContext(ctx, E.Cause(err, "write fallback prelude"))
					common.Close(dst, rawConn)
					return
				}
				_ = dst.SetWriteDeadline(time.Time{})
			}
		}

		go func() {
			_, _ = io.Copy(dst, rawConn)
			_ = dst.(*net.TCPConn).CloseWrite()
		}()
		go func() {
			_, _ = io.Copy(rawConn, dst)
			_ = rawConn.(*net.TCPConn).CloseWrite()
		}()
		return
	default:
		common.Close(rawConn)
	}
}
