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
	"testing"
	"time"
)

func TestHandshakeReplayProtector_DedupWithinTTL(t *testing.T) {
	t.Parallel()

	var nonce [kipHelloNonceSize]byte
	for i := 0; i < len(nonce); i++ {
		nonce[i] = byte(i)
	}

	p := &handshakeReplayProtector{}
	now := time.Unix(1_700_000_000, 0)

	if !p.allow("userA", nonce, now) {
		t.Fatalf("first allow must succeed")
	}
	if p.allow("userA", nonce, now.Add(1*time.Second)) {
		t.Fatalf("duplicate nonce within TTL must be rejected")
	}
	if !p.allow("userB", nonce, now.Add(1*time.Second)) {
		t.Fatalf("same nonce for different user must be allowed")
	}

	ttl := handshakeReplayTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if !p.allow("userA", nonce, now.Add(ttl+1*time.Second)) {
		t.Fatalf("nonce should be allowed after TTL")
	}
}

func TestHandshakeReplayProtector_EmptyUserHashUsesSharedBucket(t *testing.T) {
	t.Parallel()

	var nonce [kipHelloNonceSize]byte
	nonce[0] = 0x42

	p := &handshakeReplayProtector{}
	now := time.Unix(1_700_000_000, 0)

	if !p.allow("", nonce, now) {
		t.Fatalf("first allow must succeed")
	}
	if p.allow("", nonce, now.Add(1*time.Second)) {
		t.Fatalf("duplicate nonce with empty userHash must be rejected")
	}
}
