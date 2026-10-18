/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package tunnel

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing-box/transport/sudoku/crypto"
	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

const (
	handshakeTimeout = 5 * time.Second
)

// SuspiciousError indicates a potential attack or protocol violation
type SuspiciousError struct {
	Err  error
	Conn net.Conn // The connection at the state where error occurred (for fallback/logging)
}

func (e *SuspiciousError) Error() string {
	return e.Err.Error()
}

// HandshakeMeta carries optional, per-connection identity hints extracted from the client handshake.
//
// UserHash is a hex-encoded 8-byte value derived from the client's private key (when the client uses one):
// sha256(privateKey)[:8]. For clients without a private key, it is derived from the handshake nonce bytes.
type HandshakeMeta struct {
	// UserHash is a hex-encoded 8-byte client identifier.
	// When the client has a private key, it is sha256(privateKey)[:8].
	UserHash string
	// UplinkPacked reports whether the client used packed uplink obfuscation.
	UplinkPacked bool
}

func probeHandshakeBytes(probe []byte, cfg *config.Config, table *sudoku.Table, uplinkMode obfsUplinkMode) error {
	rc := &connutil.ReadOnlyConn{Reader: bytes.NewReader(probe)}
	_, obfsConn := buildObfsConnForServer(rc, table, cfg, uplinkMode, false)
	pskC2S, pskS2C := derivePSKDirectionalBases(cfg.Key)
	// Server side: recv is client->server, send is server->client.
	cConn, err := crypto.NewRecordConn(obfsConn, cfg.AEAD, pskS2C, pskC2S)
	if err != nil {
		return err
	}

	ch, err := readClientHello(cConn)
	if err != nil {
		return err
	}
	return checkClientHelloSkew(ch, time.Now())
}

// ServerHandshake authenticates an incoming connection against the configured tables and returns the
// decrypted tunnel conn plus the identity of the user that connected.
func ServerHandshake(rawConn net.Conn, cfg *config.Config, tables []*sudoku.Table) (net.Conn, *HandshakeMeta, error) {
	if rawConn == nil {
		return nil, nil, fmt.Errorf("nil conn")
	}
	if cfg == nil {
		return nil, nil, fmt.Errorf("nil config")
	}
	if userHash, ok := httpmask.EarlyHandshakeUserHash(rawConn); ok {
		uplinkPacked, _ := httpmask.EarlyHandshakeUplinkPacked(rawConn)
		return rawConn, &HandshakeMeta{UserHash: userHash, UplinkPacked: uplinkPacked}, nil
	}

	// 0) Byte-level prelude handling (legacy HTTP mask + buffered probe bytes).
	bufReader := bufio.NewReader(rawConn)
	_ = rawConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	defer func() { _ = rawConn.SetReadDeadline(time.Time{}) }()

	httpHeaderData, susp := maybeConsumeLegacyHTTPMask(rawConn, bufReader, cfg)
	if susp != nil {
		return nil, nil, susp
	}

	// 1. Sudoku Layer
	selected, preRead, err := SelectHandshakeObfsByProbe(bufReader, tables, func(probe []byte, table *sudoku.Table, uplinkMode obfsUplinkMode) error {
		return probeHandshakeBytes(probe, cfg, table, uplinkMode)
	})
	if err != nil {
		combined := make([]byte, 0, len(httpHeaderData)+len(preRead))
		combined = append(combined, httpHeaderData...)
		combined = append(combined, preRead...)
		return nil, nil, &SuspiciousError{Err: err, Conn: &recordedConn{Conn: rawConn, recorded: combined}}
	}

	baseConn := NewPreBufferedConn(rawConn, preRead)
	sConn, obfsConn := buildObfsConnForServer(baseConn, selected.Table, cfg, selected.UplinkMode, true)

	// 2. Crypto Layer
	pskC2S, pskS2C := derivePSKDirectionalBases(cfg.Key)
	// Server side: recv is client->server, send is server->client.
	cConn, err := crypto.NewRecordConn(obfsConn, cfg.AEAD, pskS2C, pskC2S)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto setup failed: %w", err)
	}

	fail := func(err error) (net.Conn, *HandshakeMeta, error) {
		return nil, nil, &SuspiciousError{Err: err, Conn: &prefixedRecorderConn{Conn: sConn, prefix: httpHeaderData}}
	}

	// 3. Handshake
	_ = rawConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	ch, err := readClientHello(cConn)
	if err != nil {
		return fail(err)
	}
	accepted, err := acceptClientHello(ch, cfg.Key, selected.Table, tables, AllowHandshakeReplay)
	if err != nil {
		return fail(err)
	}
	if accepted.table != selected.Table {
		downlink, closers := sudoku.NewServerDownlinkWriter(baseConn, accepted.table.OppositeDirection(), cfg.PaddingMin, cfg.PaddingMax, cfg.EnablePureDownlink)
		if !switchConnDownlinkWriter(obfsConn, downlink, closers...) {
			return fail(errors.New("switch downlink writer failed"))
		}
	}

	// The stream handshake answers only feature bits it knows; the early handshake echoes them all.
	accepted.reply.SelectedFeats &= KIPFeatAll
	if err := WriteKIPMessage(cConn, KIPTypeServerHello, accepted.reply.EncodePayload()); err != nil {
		return fail(fmt.Errorf("write server hello failed: %w", err))
	}
	if err := cConn.Rekey(accepted.s2c, accepted.c2s); err != nil {
		return fail(fmt.Errorf("rekey failed: %w", err))
	}

	sConn.StopRecording()
	meta := &HandshakeMeta{UserHash: accepted.userHash, UplinkPacked: selected.UplinkMode == obfsUplinkPacked}
	return wrapConnWithObfsMeta(cConn, selected.UplinkMode), meta, nil
}

func maybeConsumeLegacyHTTPMask(rawConn net.Conn, r *bufio.Reader, cfg *config.Config) ([]byte, *SuspiciousError) {
	if rawConn == nil || r == nil || cfg == nil || cfg.HTTPMask.Disable {
		return nil, nil
	}

	peekBytes, _ := r.Peek(4) // Ignore error; if peek fails, let subsequent read handle it.
	if !httpmask.LooksLikeHTTPRequestStart(peekBytes) {
		return nil, nil
	}

	consumed, err := httpmask.ConsumeHeader(r)
	if err == nil {
		return consumed, nil
	}

	// Return rawConn wrapped in bufferedConn so caller can handle fallback.
	recorder := new(bytes.Buffer)
	if len(consumed) > 0 {
		recorder.Write(consumed)
	}
	badConn := &bufferedConn{
		Conn:     rawConn,
		r:        r,
		recorder: recorder,
	}
	return consumed, &SuspiciousError{Err: fmt.Errorf("invalid http header: %w", err), Conn: badConn}
}
