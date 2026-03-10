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
	"sync"
	"time"
)

// handshakeReplayTTL bounds how long a seen nonce is remembered per user hash.
// This blocks same-window replays even when timestamps are still valid.
var handshakeReplayTTL = 60 * time.Second

type nonceSet struct {
	mu         sync.Mutex
	m          map[[kipHelloNonceSize]byte]time.Time
	maxEntries int
	lastPrune  time.Time
}

func newNonceSet(maxEntries int) *nonceSet {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	return &nonceSet{
		m:          make(map[[kipHelloNonceSize]byte]time.Time),
		maxEntries: maxEntries,
	}
}

func (s *nonceSet) allow(nonce [kipHelloNonceSize]byte, now time.Time, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	// Opportunistic prune to keep overhead bounded.
	if now.Sub(s.lastPrune) > ttl/2 || len(s.m) > s.maxEntries {
		for k, exp := range s.m {
			if !now.Before(exp) {
				delete(s.m, k)
			}
		}
		s.lastPrune = now
		// If still too big (e.g. attack), keep only the newest-ish by trimming arbitrary entries.
		for len(s.m) > s.maxEntries {
			for k := range s.m {
				delete(s.m, k)
				break
			}
		}
	}

	if exp, ok := s.m[nonce]; ok && now.Before(exp) {
		return false
	}
	s.m[nonce] = now.Add(ttl)
	return true
}

type handshakeReplayProtector struct {
	users sync.Map // map[userHash string]*nonceSet
}

func (p *handshakeReplayProtector) allow(userHash string, nonce [kipHelloNonceSize]byte, now time.Time) bool {
	if userHash == "" {
		// Anonymous: still dedupe (shared set).
		userHash = "_"
	}
	val, _ := p.users.LoadOrStore(userHash, newNonceSet(4096))
	set, ok := val.(*nonceSet)
	if !ok || set == nil {
		set = newNonceSet(4096)
		p.users.Store(userHash, set)
	}
	return set.allow(nonce, now, handshakeReplayTTL)
}

var globalHandshakeReplay = &handshakeReplayProtector{}

func AllowHandshakeReplay(userHash string, nonce [kipHelloNonceSize]byte, now time.Time) bool {
	return globalHandshakeReplay.allow(userHash, nonce, now)
}
