package tunnel

import (
	"bytes"
	"crypto/ecdh"
	"fmt"
	"net"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/crypto"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

type EarlyCodecConfig struct {
	PSK                string
	AEAD               string
	EnablePureDownlink bool
	PaddingMin         int
	PaddingMax         int
	PackedUplink       bool

	// WireWrap, server side only, wraps the raw stream below the sudoku/crypto layers
	// so wire bytes can be accounted per user.
	WireWrap func(userHash string, raw net.Conn) net.Conn
}

type earlyClientState struct {
	RequestPayload []byte

	cfg         EarlyCodecConfig
	table       *sudoku.Table
	nonce       [kipHelloNonceSize]byte
	ephemeral   *ecdh.PrivateKey
	sessionC2S  []byte
	sessionS2C  []byte
	responseSet bool
}

type earlyServerState struct {
	ResponsePayload []byte
	UserHash        string

	cfg        EarlyCodecConfig
	table      *sudoku.Table
	uplinkMode obfsUplinkMode
	sessionC2S []byte
	sessionS2C []byte
}

type earlyMemoryConn struct {
	reader *bytes.Reader
	write  bytes.Buffer
}

func newEarlyMemoryConn(readBuf []byte) *earlyMemoryConn {
	return &earlyMemoryConn{reader: bytes.NewReader(readBuf)}
}

func (c *earlyMemoryConn) Read(p []byte) (int, error) {
	if c == nil || c.reader == nil {
		return 0, net.ErrClosed
	}
	return c.reader.Read(p)
}

func (c *earlyMemoryConn) Write(p []byte) (int, error) {
	if c == nil {
		return 0, net.ErrClosed
	}
	return c.write.Write(p)
}

func (c *earlyMemoryConn) Close() error                     { return nil }
func (c *earlyMemoryConn) LocalAddr() net.Addr              { return earlyDummyAddr("local") }
func (c *earlyMemoryConn) RemoteAddr() net.Addr             { return earlyDummyAddr("remote") }
func (c *earlyMemoryConn) SetDeadline(time.Time) error      { return nil }
func (c *earlyMemoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *earlyMemoryConn) SetWriteDeadline(time.Time) error { return nil }
func (c *earlyMemoryConn) Written() []byte                  { return append([]byte(nil), c.write.Bytes()...) }

type earlyDummyAddr string

func (a earlyDummyAddr) Network() string { return string(a) }
func (a earlyDummyAddr) String() string  { return string(a) }

func buildEarlyClientObfsConn(raw net.Conn, cfg EarlyCodecConfig, table *sudoku.Table) net.Conn {
	uplinkMode := obfsUplinkPure
	if cfg.PackedUplink {
		uplinkMode = obfsUplinkPacked
	}
	return buildClientObfsConnForMode(raw, table, cfg.PaddingMin, cfg.PaddingMax, cfg.EnablePureDownlink, uplinkMode)
}

func buildEarlyServerObfsConn(raw net.Conn, cfg EarlyCodecConfig, table *sudoku.Table, uplinkMode obfsUplinkMode) net.Conn {
	_, conn := buildServerObfsConnForMode(raw, table, cfg.PaddingMin, cfg.PaddingMax, cfg.EnablePureDownlink, uplinkMode, false)
	return conn
}

func newEarlyClientState(cfg EarlyCodecConfig, table *sudoku.Table, tableHint uint32, hasTableHint bool, userHash [kipHelloUserHashSize]byte, feats uint32) (*earlyClientState, error) {
	if table == nil {
		return nil, fmt.Errorf("nil table")
	}

	hello, ephemeral, err := newClientHello(userHash, feats, tableHint, hasTableHint)
	if err != nil {
		return nil, err
	}

	mem := newEarlyMemoryConn(nil)
	obfsConn := buildEarlyClientObfsConn(mem, cfg, table)
	pskC2S, pskS2C := derivePSKDirectionalBases(cfg.PSK)
	rc, err := crypto.NewRecordConn(obfsConn, cfg.AEAD, pskC2S, pskS2C)
	if err != nil {
		return nil, fmt.Errorf("client early crypto setup failed: %w", err)
	}
	if err := WriteKIPMessage(rc, KIPTypeClientHello, hello.EncodePayload()); err != nil {
		return nil, fmt.Errorf("write early client hello failed: %w", err)
	}

	return &earlyClientState{
		RequestPayload: mem.Written(),
		cfg:            cfg,
		table:          table,
		nonce:          hello.Nonce,
		ephemeral:      ephemeral,
	}, nil
}

func (s *earlyClientState) ProcessResponse(payload []byte) error {
	if s == nil {
		return fmt.Errorf("nil client state")
	}
	mem := newEarlyMemoryConn(payload)
	obfsConn := buildEarlyClientObfsConn(mem, s.cfg, s.table)
	pskC2S, pskS2C := derivePSKDirectionalBases(s.cfg.PSK)
	rc, err := crypto.NewRecordConn(obfsConn, s.cfg.AEAD, pskC2S, pskS2C)
	if err != nil {
		return fmt.Errorf("client early crypto setup failed: %w", err)
	}

	sh, err := readServerHello(rc)
	if err != nil {
		return err
	}
	s.sessionC2S, s.sessionS2C, err = deriveClientSession(s.cfg.PSK, s.ephemeral, s.nonce, sh)
	if err != nil {
		return err
	}
	s.responseSet = true
	return nil
}

func (s *earlyClientState) WrapConn(raw net.Conn) (net.Conn, error) {
	if s == nil {
		return nil, fmt.Errorf("nil client state")
	}
	if !s.responseSet {
		return nil, fmt.Errorf("early handshake not completed")
	}
	obfsConn := buildEarlyClientObfsConn(raw, s.cfg, s.table)
	rc, err := crypto.NewRecordConn(obfsConn, s.cfg.AEAD, s.sessionC2S, s.sessionS2C)
	if err != nil {
		return nil, fmt.Errorf("setup client session crypto failed: %w", err)
	}
	return rc, nil
}

func (s *earlyClientState) Ready() bool {
	return s != nil && s.responseSet
}

func NewHTTPMaskClientEarlyHandshake(cfg EarlyCodecConfig, table *sudoku.Table, tableHint uint32, hasTableHint bool, userHash [kipHelloUserHashSize]byte, feats uint32) (*httpmask.ClientEarlyHandshake, error) {
	state, err := newEarlyClientState(cfg, table, tableHint, hasTableHint, userHash, feats)
	if err != nil {
		return nil, err
	}
	return &httpmask.ClientEarlyHandshake{
		RequestPayload: state.RequestPayload,
		HandleResponse: state.ProcessResponse,
		Ready:          state.Ready,
		WrapConn:       state.WrapConn,
	}, nil
}

func ProcessEarlyClientPayload(cfg EarlyCodecConfig, tables []*sudoku.Table, payload []byte, allowReplay ReplayAllowFunc) (*earlyServerState, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty early payload")
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("no tables configured")
	}

	var firstErr error
	for _, table := range tables {
		for _, uplinkMode := range []obfsUplinkMode{obfsUplinkPure, obfsUplinkPacked} {
			state, err := processEarlyClientPayloadForTable(cfg, tables, table, uplinkMode, payload, allowReplay)
			if err == nil {
				return state, nil
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("early handshake probe failed")
	}
	return nil, firstErr
}

func processEarlyClientPayloadForTable(cfg EarlyCodecConfig, tables []*sudoku.Table, table *sudoku.Table, uplinkMode obfsUplinkMode, payload []byte, allowReplay ReplayAllowFunc) (*earlyServerState, error) {
	mem := newEarlyMemoryConn(payload)
	obfsConn := buildEarlyServerObfsConn(mem, cfg, table, uplinkMode)
	pskC2S, pskS2C := derivePSKDirectionalBases(cfg.PSK)
	rc, err := crypto.NewRecordConn(obfsConn, cfg.AEAD, pskS2C, pskC2S)
	if err != nil {
		return nil, err
	}

	ch, err := readClientHello(rc)
	if err != nil {
		return nil, err
	}
	accepted, err := acceptClientHello(ch, cfg.PSK, table, tables, allowReplay)
	if err != nil {
		return nil, err
	}

	respMem := newEarlyMemoryConn(nil)
	respObfs := buildEarlyServerObfsConn(respMem, cfg, accepted.table, uplinkMode)
	respConn, err := crypto.NewRecordConn(respObfs, cfg.AEAD, pskS2C, pskC2S)
	if err != nil {
		return nil, fmt.Errorf("server early crypto setup failed: %w", err)
	}
	if err := WriteKIPMessage(respConn, KIPTypeServerHello, accepted.reply.EncodePayload()); err != nil {
		return nil, fmt.Errorf("write early server hello failed: %w", err)
	}

	return &earlyServerState{
		ResponsePayload: respMem.Written(),
		UserHash:        accepted.userHash,
		cfg:             cfg,
		table:           accepted.table,
		uplinkMode:      uplinkMode,
		sessionC2S:      accepted.c2s,
		sessionS2C:      accepted.s2c,
	}, nil
}

func (s *earlyServerState) WrapConn(raw net.Conn) (net.Conn, error) {
	if s == nil {
		return nil, fmt.Errorf("nil server state")
	}
	if s.cfg.WireWrap != nil {
		raw = s.cfg.WireWrap(s.UserHash, raw)
	}
	obfsConn := buildEarlyServerObfsConn(raw, s.cfg, s.table, s.uplinkMode)
	rc, err := crypto.NewRecordConn(obfsConn, s.cfg.AEAD, s.sessionS2C, s.sessionC2S)
	if err != nil {
		return nil, fmt.Errorf("setup server session crypto failed: %w", err)
	}
	return wrapConnWithObfsMeta(rc, s.uplinkMode), nil
}

func NewHTTPMaskServerEarlyHandshake(cfg EarlyCodecConfig, tables []*sudoku.Table, allowReplay ReplayAllowFunc) *httpmask.TunnelServerEarlyHandshake {
	return &httpmask.TunnelServerEarlyHandshake{
		Prepare: func(payload []byte) (*httpmask.PreparedServerEarlyHandshake, error) {
			state, err := ProcessEarlyClientPayload(cfg, tables, payload, allowReplay)
			if err != nil {
				return nil, err
			}
			return &httpmask.PreparedServerEarlyHandshake{
				ResponsePayload: state.ResponsePayload,
				WrapConn:        state.WrapConn,
				UserHash:        state.UserHash,
			}, nil
		},
	}
}
