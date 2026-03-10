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
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
)

func derivePSKDirectionalBases(psk string) (c2s, s2c []byte) {
	sum := sha256.Sum256([]byte(psk))
	c2sKey, _ := hkdf.Expand(sha256.New, sum[:], "sudoku-psk-c2s", 32)
	s2cKey, _ := hkdf.Expand(sha256.New, sum[:], "sudoku-psk-s2c", 32)
	return c2sKey, s2cKey
}

func DerivePSKDirectionalBases(psk string) (c2s, s2c []byte) {
	return derivePSKDirectionalBases(psk)
}

func deriveSessionDirectionalBases(psk string, shared []byte, nonce [kipHelloNonceSize]byte) (c2s, s2c []byte, err error) {
	sum := sha256.Sum256([]byte(psk))
	ikm := make([]byte, 0, len(shared)+len(nonce))
	ikm = append(ikm, shared...)
	ikm = append(ikm, nonce[:]...)

	prk, err := hkdf.Extract(sha256.New, ikm, sum[:])
	if err != nil {
		return nil, nil, fmt.Errorf("hkdf extract: %w", err)
	}

	c2sKey, err := hkdf.Expand(sha256.New, prk, "sudoku-session-c2s", 32)
	if err != nil {
		return nil, nil, fmt.Errorf("hkdf expand c2s: %w", err)
	}
	s2cKey, err := hkdf.Expand(sha256.New, prk, "sudoku-session-s2c", 32)
	if err != nil {
		return nil, nil, fmt.Errorf("hkdf expand s2c: %w", err)
	}
	return c2sKey, s2cKey, nil
}

func DeriveSessionDirectionalBases(psk string, shared []byte, nonce [16]byte) (c2s, s2c []byte, err error) {
	return deriveSessionDirectionalBases(psk, shared, nonce)
}

func x25519SharedSecret(priv *ecdh.PrivateKey, peerPub []byte) ([]byte, error) {
	if priv == nil {
		return nil, fmt.Errorf("nil priv")
	}
	curve := ecdh.X25519()
	pk, err := curve.NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("parse peer pub: %w", err)
	}
	secret, err := priv.ECDH(pk)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}
	return secret, nil
}

func X25519SharedSecret(priv *ecdh.PrivateKey, peerPub []byte) ([]byte, error) {
	return x25519SharedSecret(priv, peerPub)
}
