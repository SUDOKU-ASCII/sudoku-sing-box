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
	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/udpnat"
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
	reverse   *sudokut.ReverseManager
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SudokuInboundOptions) (adapter.Inbound, error) {
	if options.Key == "" {
		return nil, E.New("missing key")
	}

	defaultConf := sudokut.DefaultConfig()
	paddingMin, paddingMax := resolvePadding(defaultConf.PaddingMin, defaultConf.PaddingMax, options.PaddingMin, options.PaddingMax)
	multiplex := resolveMultiplex(defaultConf.Multiplex, options.Multiplex, options.HTTPMaskMultiplex)

	protoConf := sudokut.ProtocolConfig{
		Key:                     options.Key,
		AEADMethod:              defaultConf.AEADMethod,
		PaddingMin:              paddingMin,
		PaddingMax:              paddingMax,
		EnablePureDownlink:      resolveBool(defaultConf.EnablePureDownlink, options.EnablePureDownlink),
		Multiplex:               multiplex,
		HandshakeTimeoutSeconds: defaultConf.HandshakeTimeoutSeconds,
		SuspiciousAction:        defaultConf.SuspiciousAction,
		FallbackAddress:         options.FallbackAddress,
		DisableHTTPMask:         options.DisableHTTPMask,
		HTTPMaskMode:            resolveHTTPMaskMode(defaultConf.HTTPMaskMode, options.HTTPMaskMode, ""),
		HTTPMaskMultiplex:       options.HTTPMaskMultiplex,
		HTTPMaskPathRoot:        options.HTTPMaskPathRoot,
	}
	for _, user := range options.Users {
		protoConf.Users = append(protoConf.Users, sudokut.UserConfig{
			Alias:     user.Alias,
			Key:       user.Key,
			Bandwidth: user.Bandwidth,
			Data:      user.Data,
			Blocked:   user.Blocked,
		})
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

	tables, err := buildProtocolTables(protoConf.Key, options.ASCII, options.CustomTable, options.CustomTables, false)
	if err != nil {
		return nil, E.Cause(err, "build table(s)")
	}
	applyProtocolTables(&protoConf, tables)

	tunnelSrv, err := sudokut.NewHTTPMaskTunnelServer(&protoConf)
	if err != nil {
		return nil, E.Cause(err, "create tunnel server")
	}

	var reverseManager *sudokut.ReverseManager
	if strings.TrimSpace(options.Reverse.Listen) != "" {
		reverseManager, err = sudokut.NewReverseManager(options.Reverse.Listen)
		if err != nil {
			return nil, E.Cause(err, "create reverse entry")
		}
	}

	in := &Inbound{
		Adapter:   inbound.NewAdapter(C.TypeSudoku, tag),
		ctx:       ctx,
		router:    router,
		logger:    logger,
		protoConf: protoConf,
		tunnelSrv: tunnelSrv,
		reverse:   reverseManager,
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

func (h *Inbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	// Scope cleanups run in reverse order: stop the listener first, then the tunnel server and the reverse entry.
	if h.reverse != nil {
		scope.Add(h.reverse.Close)
		go func() {
			if err := h.reverse.Serve(); err != nil {
				h.logger.ErrorContext(h.ctx, E.Cause(err, "serve Sudoku reverse entry"))
			}
		}()
	}
	scope.Add(h.tunnelSrv.Close)
	if err := h.listener.Start(); err != nil {
		return err
	}
	scope.Add(h.listener.Close)
	return nil
}

func (h *Inbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	sessionConn, session, targetAddr, userHash, payload, handled, err := h.tunnelSrv.HandleConnSessionAutoWithUserHash(conn)
	if err != nil {
		var suspErr *sudokut.SuspiciousError
		if errors.As(err, &suspErr) {
			h.handleSuspicious(ctx, suspErr, &h.protoConf, metadata.Source, onClose)
			return
		}
		var rejectedErr *sudokut.UserRejectedError
		if errors.As(err, &rejectedErr) {
			N.CloseOnHandshakeFailure(conn, onClose, err)
			h.logger.WarnContext(ctx, E.Cause(err, "process connection from ", metadata.Source))
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

	if h.tunnelSrv.HasUsers() {
		h.logger.InfoContext(ctx, "inbound Sudoku user ", h.tunnelSrv.UserLabel(userHash), " from ", metadata.Source)
	} else if userHash != "" {
		h.logger.DebugContext(ctx, "sudoku user=", userHash)
	}

	switch session {
	case sudokut.SessionUoT:
		h.logger.InfoContext(ctx, "inbound Sudoku UoT session from ", metadata.Source)
		h.serveUoT(ctx, sudokut.NewUoTPacketConn(sessionConn), metadata, onClose)
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
	case sudokut.SessionReverse:
		if h.reverse == nil {
			_ = sessionConn.Close()
			h.logger.WarnContext(ctx, "drop Sudoku reverse session from ", metadata.Source, ": reverse entry is not configured")
			return
		}
		h.logger.InfoContext(ctx, "inbound Sudoku reverse session from ", metadata.Source)
		err = h.reverse.HandleServerSession(sessionConn, payload)
		if err != nil {
			h.logger.ErrorContext(ctx, E.Cause(err, "process reverse session from ", metadata.Source))
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

func (h *Inbound) serveUoT(ctx context.Context, conn *sudokut.UoTPacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		_ = conn.Close()
		if onClose != nil {
			onClose(nil)
		}
	}()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	handler := &uotPacketHandler{router: h.router, metadata: metadata}
	nat := udpnat.NewEx[M.Socksaddr](int64(C.UDPTimeout.Seconds()), handler)
	for {
		buffer := buf.NewPacket()
		destination, err := conn.ReadPacket(buffer)
		if err != nil {
			buffer.Release()
			if !errors.Is(err, io.EOF) && !E.IsClosedOrCanceled(err) {
				h.logger.ErrorContext(ctx, E.Cause(err, "read Sudoku UoT packet"))
			}
			return
		}
		nat.NewPacketEx(ctx, destination, buffer, metadata.Source, destination, func(N.PacketConn) N.PacketWriter {
			return uotPacketWriter{conn}
		})
	}
}

type uotPacketHandler struct {
	router   adapter.ConnectionRouterEx
	metadata adapter.InboundContext
}

func (h *uotPacketHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	metadata := h.metadata
	metadata.Source = source
	metadata.Destination = destination
	h.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}

type uotPacketWriter struct {
	conn *sudokut.UoTPacketConn
}

func (w uotPacketWriter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return w.conn.WritePacket(buffer, destination)
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
				if err := connutil.WriteFull(dst, badData); err != nil {
					h.logger.ErrorContext(ctx, E.Cause(err, "write fallback prelude"))
					common.Close(dst, rawConn)
					return
				}
				_ = dst.SetWriteDeadline(time.Time{})
			}
		}

		relaySuspiciousPair(rawConn, dst)
		return
	default:
		common.Close(rawConn)
	}
}

func relaySuspiciousPair(client, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		relaySuspiciousConn(upstream, client)
	}()
	go func() {
		defer wg.Done()
		relaySuspiciousConn(client, upstream)
	}()
	wg.Wait()
	common.Close(client, upstream)
}

func relaySuspiciousConn(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = connutil.TryCloseWrite(dst)
}
