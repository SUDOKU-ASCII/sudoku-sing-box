package sudoku

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/crypto"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

type SessionType int

const (
	SessionTypeTCP SessionType = iota
	SessionTypeUoT
	SessionTypeMux
)

type ServerSession struct {
	Conn     net.Conn
	Type     SessionType
	Target   string
	UserHash string
}

type preBufferedConn struct {
	net.Conn
	buf []byte
}

func (p *preBufferedConn) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	if p.Conn == nil {
		return 0, io.EOF
	}
	return p.Conn.Read(b)
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

const (
	downlinkModePure   byte = 0x01
	downlinkModePacked byte = 0x02
)

func downlinkMode(cfg *ProtocolConfig) byte {
	if cfg.EnablePureDownlink {
		return downlinkModePure
	}
	return downlinkModePacked
}

func buildClientObfsConn(raw net.Conn, cfg *ProtocolConfig, table *sudoku.Table) net.Conn {
	baseSudoku := sudoku.NewConn(raw, table, cfg.PaddingMin, cfg.PaddingMax, false)
	if cfg.EnablePureDownlink {
		return baseSudoku
	}
	packed := sudoku.NewPackedConn(raw, table, cfg.PaddingMin, cfg.PaddingMax)
	return sudoku.NewDirectionalConn(raw, packed, baseSudoku)
}

func buildServerObfsConn(raw net.Conn, cfg *ProtocolConfig, table *sudoku.Table, record bool) (*sudoku.Conn, net.Conn) {
	uplinkSudoku := sudoku.NewConn(raw, table, cfg.PaddingMin, cfg.PaddingMax, record)
	if cfg.EnablePureDownlink {
		return uplinkSudoku, uplinkSudoku
	}
	packed := sudoku.NewPackedConn(raw, table, cfg.PaddingMin, cfg.PaddingMax)
	return uplinkSudoku, sudoku.NewDirectionalConn(raw, uplinkSudoku, packed, packed.Flush)
}

func buildHandshakePayload(key string) [16]byte {
	var payload [16]byte
	binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().Unix()))
	if keyBytes, ok := crypto.DecodePrivateKeyBytes(key); ok {
		hash := sha256.Sum256(keyBytes)
		copy(payload[8:], hash[:8])
		return payload
	}
	if _, err := rand.Read(payload[8:]); err != nil {
		binary.BigEndian.PutUint64(payload[8:], uint64(time.Now().UnixNano()))
	}
	return payload
}

func userHashFromHandshake(handshakeBuf []byte) string {
	if len(handshakeBuf) < 16 {
		return ""
	}
	return hex.EncodeToString(handshakeBuf[8:16])
}

type ClientHandshakeOptions struct {
	HTTPMaskStrategy string
}

func ClientHandshake(rawConn net.Conn, cfg *ProtocolConfig) (net.Conn, error) {
	return ClientHandshakeWithOptions(rawConn, cfg, ClientHandshakeOptions{})
}

func ClientHandshakeWithOptions(rawConn net.Conn, cfg *ProtocolConfig, opt ClientHandshakeOptions) (net.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if !cfg.DisableHTTPMask {
		if err := WriteHTTPMaskHeader(rawConn, cfg.ServerAddress, cfg.HTTPMaskPathRoot, opt.HTTPMaskStrategy); err != nil {
			return nil, fmt.Errorf("write http mask failed: %w", err)
		}
	}

	table, err := pickClientTable(cfg)
	if err != nil {
		return nil, err
	}

	obfsConn := buildClientObfsConn(rawConn, cfg, table)
	cConn, err := crypto.NewAEADConn(obfsConn, ClientAEADSeed(cfg.Key), cfg.AEADMethod)
	if err != nil {
		return nil, fmt.Errorf("setup crypto failed: %w", err)
	}

	handshake := buildHandshakePayload(cfg.Key)
	if _, err := cConn.Write(handshake[:]); err != nil {
		cConn.Close()
		return nil, fmt.Errorf("send handshake failed: %w", err)
	}
	if _, err := cConn.Write([]byte{downlinkMode(cfg)}); err != nil {
		cConn.Close()
		return nil, fmt.Errorf("send downlink mode failed: %w", err)
	}

	return cConn, nil
}

func ServerHandshake(rawConn net.Conn, cfg *ProtocolConfig) (*ServerSession, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	handshakeTimeout := time.Duration(cfg.HandshakeTimeoutSeconds) * time.Second
	if handshakeTimeout <= 0 {
		handshakeTimeout = 5 * time.Second
	}

	bufReader := bufio.NewReader(rawConn)
	recordedBytes := func(chunks ...[]byte) []byte {
		total := 0
		for _, b := range chunks {
			total += len(b)
		}
		out := make([]byte, 0, total)
		for _, b := range chunks {
			out = append(out, b...)
		}
		return out
	}
	suspicious := func(captured error, chunks ...[]byte) (*ServerSession, error) {
		return nil, &SuspiciousError{
			Err:  captured,
			Conn: &recordedConn{Conn: rawConn, recorded: recordedBytes(chunks...)},
		}
	}

	_ = rawConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var httpHeaderData []byte
	if !cfg.DisableHTTPMask {
		if peek, err := bufReader.Peek(4); err == nil && httpmask.LooksLikeHTTPRequestStart(peek) {
			consumed, err := httpmask.ConsumeHeader(bufReader)
			httpHeaderData = consumed
			if err != nil {
				peeked, _ := bufReader.Peek(bufReader.Buffered())
				_ = rawConn.SetReadDeadline(time.Time{})
				return suspicious(fmt.Errorf("invalid http header: %w", err), httpHeaderData, peeked)
			}
		}
	}

	selectedTable, preRead, err := selectTableByProbe(bufReader, cfg, cfg.tableCandidates())
	_ = rawConn.SetReadDeadline(time.Time{})
	if err != nil {
		return suspicious(err, httpHeaderData, preRead)
	}

	baseConn := &preBufferedConn{Conn: rawConn, buf: preRead}
	sConn, obfsConn := buildServerObfsConn(baseConn, cfg, selectedTable, true)
	defer sConn.StopRecording()
	cConn, err := crypto.NewAEADConn(obfsConn, cfg.Key, cfg.AEADMethod)
	if err != nil {
		return nil, fmt.Errorf("crypto setup failed: %w", err)
	}

	_ = rawConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	defer rawConn.SetReadDeadline(time.Time{})

	var handshakeBuf [16]byte
	if _, err := io.ReadFull(cConn, handshakeBuf[:]); err != nil {
		return suspicious(fmt.Errorf("read handshake failed: %w", err), httpHeaderData, sConn.GetBufferedAndRecorded())
	}

	ts := int64(binary.BigEndian.Uint64(handshakeBuf[:8]))
	if absInt64(time.Now().Unix()-ts) > 60 {
		return suspicious(fmt.Errorf("timestamp skew detected"), httpHeaderData, sConn.GetBufferedAndRecorded())
	}
	userHash := userHashFromHandshake(handshakeBuf[:])

	modeBuf := []byte{0}
	if _, err := io.ReadFull(cConn, modeBuf); err != nil {
		return suspicious(fmt.Errorf("read downlink mode failed: %w", err), httpHeaderData, sConn.GetBufferedAndRecorded())
	}
	if modeBuf[0] != downlinkMode(cfg) {
		return suspicious(fmt.Errorf("downlink mode mismatch: client=%d server=%d", modeBuf[0], downlinkMode(cfg)), httpHeaderData, sConn.GetBufferedAndRecorded())
	}

	firstByte := make([]byte, 1)
	if _, err := io.ReadFull(cConn, firstByte); err != nil {
		return suspicious(fmt.Errorf("read first byte failed: %w", err), httpHeaderData, sConn.GetBufferedAndRecorded())
	}

	if firstByte[0] == MuxMagicByte {
		version := make([]byte, 1)
		if _, err := io.ReadFull(cConn, version); err != nil {
			return suspicious(fmt.Errorf("read mux version failed: %w", err), httpHeaderData, sConn.GetBufferedAndRecorded())
		}
		if version[0] != muxVersion {
			return suspicious(fmt.Errorf("unsupported mux version: %d", version[0]), httpHeaderData, sConn.GetBufferedAndRecorded())
		}
		return &ServerSession{Conn: cConn, Type: SessionTypeMux, UserHash: userHash}, nil
	}

	if firstByte[0] == UoTMagicByte {
		version := make([]byte, 1)
		if _, err := io.ReadFull(cConn, version); err != nil {
			return suspicious(fmt.Errorf("read uot version failed: %w", err), httpHeaderData, sConn.GetBufferedAndRecorded())
		}
		if version[0] != uotVersion {
			return suspicious(fmt.Errorf("unsupported uot version: %d", version[0]), httpHeaderData, sConn.GetBufferedAndRecorded())
		}
		return &ServerSession{Conn: cConn, Type: SessionTypeUoT, UserHash: userHash}, nil
	}

	prefixed := &preBufferedConn{Conn: cConn, buf: firstByte}
	target, err := DecodeAddress(prefixed)
	if err != nil {
		return suspicious(fmt.Errorf("read target address failed: %w", err), httpHeaderData, sConn.GetBufferedAndRecorded())
	}
	return &ServerSession{
		Conn:     prefixed,
		Type:     SessionTypeTCP,
		Target:   target,
		UserHash: userHash,
	}, nil
}

func randomByte() byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err == nil {
		return b[0]
	}
	return byte(time.Now().UnixNano())
}
