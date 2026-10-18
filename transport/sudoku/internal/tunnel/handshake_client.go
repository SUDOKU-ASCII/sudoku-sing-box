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
	"fmt"
	"net"

	"github.com/sagernet/sing-box/transport/sudoku/crypto"
	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func clientHandshake(rc *crypto.RecordConn, seed string, userHash [kipHelloUserHashSize]byte, feats uint32, tableHint uint32, hasTableHint bool) (selectedFeats uint32, err error) {
	if rc == nil {
		return 0, fmt.Errorf("nil conn")
	}
	ch, ephemeral, err := newClientHello(userHash, feats, tableHint, hasTableHint)
	if err != nil {
		return 0, err
	}
	if err := WriteKIPMessage(rc, KIPTypeClientHello, ch.EncodePayload()); err != nil {
		return 0, fmt.Errorf("write client hello failed: %w", err)
	}
	sh, err := readServerHello(rc)
	if err != nil {
		return 0, err
	}
	sessC2S, sessS2C, err := deriveClientSession(seed, ephemeral, ch.Nonce, sh)
	if err != nil {
		return 0, err
	}
	if err := rc.Rekey(sessC2S, sessS2C); err != nil {
		return 0, fmt.Errorf("rekey failed: %w", err)
	}
	return sh.SelectedFeats, nil
}

func ClientHandshakeWithUplinkMode(conn net.Conn, cfg *config.Config, table *sudoku.Table, privateKey []byte, uplinkMode obfsUplinkMode, tableHint uint32, hasTableHint bool) (net.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if table == nil {
		return nil, fmt.Errorf("nil table")
	}
	var obfsConn net.Conn
	switch uplinkMode {
	case obfsUplinkPacked:
		obfsConn = buildReverseObfsConnForClient(conn, table, cfg)
	default:
		obfsConn = buildObfsConnForClient(conn, table, cfg)
	}

	pskC2S, pskS2C := derivePSKDirectionalBases(cfg.Key)
	rc, err := crypto.NewRecordConn(obfsConn, cfg.AEAD, pskC2S, pskS2C)
	if err != nil {
		return nil, fmt.Errorf("crypto setup failed: %w", err)
	}

	userHash := kipUserHashFromPrivateKey(privateKey, cfg.Key)
	if _, err := clientHandshake(rc, cfg.Key, userHash, KIPFeatAll, tableHint, hasTableHint); err != nil {
		_ = rc.Close()
		return nil, err
	}
	return rc, nil
}
