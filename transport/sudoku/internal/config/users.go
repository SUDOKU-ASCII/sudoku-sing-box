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
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

const (
	// UserHashHexLen is the length of the hex-encoded user hash sent in the handshake (sha256(privateKey)[:8]).
	UserHashHexLen = 16
	// UserMinPrefixHexLen is the shortest accepted user hash prefix.
	UserMinPrefixHexLen = 8
)

// UserConfig describes one whitelisted user.
//
// Key is either a client private key (hex, 32 or 64 bytes) or a prefix (8..16 hex chars)
// of the user hash sha256(privateKey)[:8]; the two are told apart by length.
type UserConfig struct {
	Alias string `json:"alias,omitempty"`
	Key   string `json:"key"`
	// Bandwidth is [server->client, client->server] in Mbps of payload rate; 0 means unlimited.
	Bandwidth []float64 `json:"bandwidth,omitempty"`
	// Data is the total wire traffic limit (tx+rx) in GB (1e9 bytes); 0 means unlimited.
	Data    float64 `json:"data,omitempty"`
	Blocked bool    `json:"blocked,omitempty"`
}

// UserHashFromKey returns the lower-case hex user hash (or hash prefix) that identifies key,
// and whether key was a full private key. The hash is sha256 over the decoded private key bytes,
// matching what clients put in the handshake.
func UserHashFromKey(key string) (hash string, isPrivateKey bool, err error) {
	key = strings.ToLower(strings.TrimSpace(key))
	for i := 0; i < len(key); i++ {
		if c := key[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", false, fmt.Errorf("key is not valid hex")
		}
	}
	switch n := len(key); {
	case n == 64 || n == 128:
		raw, err := hex.DecodeString(key)
		if err != nil {
			return "", false, err
		}
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:8]), true, nil
	case n >= UserMinPrefixHexLen && n <= UserHashHexLen:
		return key, false, nil
	default:
		return "", false, fmt.Errorf("key must be a private key (64 or 128 hex chars) or a user hash prefix (%d-%d hex chars), got %d chars", UserMinPrefixHexLen, UserHashHexLen, n)
	}
}

func (c *Config) finalizeUsers() error {
	if len(c.Users) == 0 {
		return nil
	}
	seen := make(map[string]int, len(c.Users))
	for i := range c.Users {
		u := &c.Users[i]
		u.Key = strings.TrimSpace(u.Key)
		u.Alias = strings.TrimSpace(u.Alias)

		hash, _, err := UserHashFromKey(u.Key)
		if err != nil {
			return fmt.Errorf("users[%d]: %w", i, err)
		}
		if prev, dup := seen[hash]; dup {
			return fmt.Errorf("users[%d]: duplicate of users[%d]", i, prev)
		}
		seen[hash] = i

		if u.Alias == "" {
			// The hash, not the key: a private key must never end up in logs.
			u.Alias = hash
		}

		if n := len(u.Bandwidth); n != 0 && n != 2 {
			return fmt.Errorf("users[%d]: bandwidth must have exactly 2 elements [server->client, client->server] in Mbps", i)
		}
		for _, v := range u.Bandwidth {
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("users[%d]: bandwidth must be >= 0", i)
			}
		}
		if u.Data < 0 || math.IsNaN(u.Data) || math.IsInf(u.Data, 0) {
			return fmt.Errorf("users[%d]: data must be >= 0", i)
		}
	}
	return nil
}
