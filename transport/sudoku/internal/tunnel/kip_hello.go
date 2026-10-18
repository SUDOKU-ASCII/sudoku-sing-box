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
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

// The hello exchange is shared by the stream handshake (handshake_kip.go, server.go) and the
// HTTP-mask early handshake (early.go): both run the same checks and key derivation and differ
// only in how the messages travel.

// kipHandshakeSkew is the clock difference tolerated between a ClientHello and the server.
const kipHandshakeSkew = 60 * time.Second

// ReplayAllowFunc reports whether a (user, nonce) pair is fresh; it must remember accepted pairs.
type ReplayAllowFunc func(userHash string, nonce [kipHelloNonceSize]byte, now time.Time) bool

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// newClientHello builds a ClientHello with a fresh nonce and X25519 key. The returned private
// key completes the key exchange once the ServerHello arrives.
func newClientHello(userHash [kipHelloUserHashSize]byte, feats, tableHint uint32, hasTableHint bool) (*KIPClientHello, *ecdh.PrivateKey, error) {
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("ecdh generate failed: %w", err)
	}
	ch := &KIPClientHello{
		Timestamp:    time.Now(),
		UserHash:     userHash,
		Features:     feats,
		TableHint:    tableHint,
		HasTableHint: hasTableHint,
	}
	if _, err := io.ReadFull(rand.Reader, ch.Nonce[:]); err != nil {
		return nil, nil, fmt.Errorf("nonce generate failed: %w", err)
	}
	copy(ch.ClientPub[:], ephemeral.PublicKey().Bytes())
	return ch, ephemeral, nil
}

func readClientHello(r io.Reader) (*KIPClientHello, error) {
	msg, err := ReadKIPMessage(r)
	if err != nil {
		return nil, fmt.Errorf("handshake read failed: %w", err)
	}
	if msg.Type != KIPTypeClientHello {
		return nil, fmt.Errorf("unexpected handshake message: %d", msg.Type)
	}
	ch, err := DecodeKIPClientHelloPayload(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode client hello failed: %w", err)
	}
	return ch, nil
}

func readServerHello(r io.Reader) (*KIPServerHello, error) {
	msg, err := ReadKIPMessage(r)
	if err != nil {
		return nil, fmt.Errorf("read server hello failed: %w", err)
	}
	if msg.Type != KIPTypeServerHello {
		return nil, fmt.Errorf("unexpected handshake message: %d", msg.Type)
	}
	sh, err := DecodeKIPServerHelloPayload(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode server hello failed: %w", err)
	}
	return sh, nil
}

// checkClientHelloSkew rejects a ClientHello whose timestamp is too far from now.
func checkClientHelloSkew(ch *KIPClientHello, now time.Time) error {
	if absInt64(now.Unix()-ch.Timestamp.Unix()) > int64(kipHandshakeSkew.Seconds()) {
		return errors.New("time skew/replay")
	}
	return nil
}

// deriveClientSession checks that sh answers the hello carrying nonce and derives the session
// key bases (client-to-server, server-to-client).
func deriveClientSession(psk string, ephemeral *ecdh.PrivateKey, nonce [kipHelloNonceSize]byte, sh *KIPServerHello) (c2s, s2c []byte, err error) {
	if sh.Nonce != nonce {
		return nil, nil, errors.New("handshake nonce mismatch")
	}
	shared, err := x25519SharedSecret(ephemeral, sh.ServerPub[:])
	if err != nil {
		return nil, nil, fmt.Errorf("ecdh failed: %w", err)
	}
	c2s, s2c, err = deriveSessionDirectionalBases(psk, shared, nonce)
	if err != nil {
		return nil, nil, fmt.Errorf("derive session keys failed: %w", err)
	}
	return c2s, s2c, nil
}

// acceptedHello is the server's decision on a valid ClientHello: its reply and the session keys.
type acceptedHello struct {
	userHash string
	// table is the table the client asked for; it differs from the probed table only when a
	// table hint selects another ASCII-compatible table.
	table *sudoku.Table
	// reply echoes every feature bit the client offered; callers narrow it where required.
	reply    *KIPServerHello
	c2s, s2c []byte
}

// acceptClientHello validates ch (freshness, replay, table hint) and answers its key exchange.
// allow may be nil to skip replay protection.
func acceptClientHello(ch *KIPClientHello, psk string, probed *sudoku.Table, tables []*sudoku.Table, allow ReplayAllowFunc) (*acceptedHello, error) {
	now := time.Now()
	if err := checkClientHelloSkew(ch, now); err != nil {
		return nil, err
	}
	userHash := kipUserHashHex(ch.UserHash)
	if allow != nil && !allow(userHash, ch.Nonce, now) {
		return nil, errors.New("replay detected")
	}
	table, err := ResolveClientHelloTable(probed, tables, ch)
	if err != nil {
		return nil, fmt.Errorf("resolve table hint failed: %w", err)
	}

	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ecdh generate failed: %w", err)
	}
	shared, err := x25519SharedSecret(ephemeral, ch.ClientPub[:])
	if err != nil {
		return nil, fmt.Errorf("ecdh failed: %w", err)
	}
	c2s, s2c, err := deriveSessionDirectionalBases(psk, shared, ch.Nonce)
	if err != nil {
		return nil, fmt.Errorf("derive session keys failed: %w", err)
	}

	reply := &KIPServerHello{Nonce: ch.Nonce, SelectedFeats: ch.Features}
	copy(reply.ServerPub[:], ephemeral.PublicKey().Bytes())
	return &acceptedHello{userHash: userHash, table: table, reply: reply, c2s: c2s, s2c: s2c}, nil
}
