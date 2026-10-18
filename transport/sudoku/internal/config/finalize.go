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
	"fmt"
	"strings"
)

// Finalize normalizes and validates cross-field settings after loading/unmarshalling.
func (c *Config) Finalize() error {
	if c == nil {
		return fmt.Errorf("nil config")
	}

	c.normalizeFields()
	c.finalizeHTTPMask()

	if c.Reverse != nil {
		if err := c.Reverse.finalize(); err != nil {
			return err
		}
		if c.Reverse.isEmpty() {
			c.Reverse = nil
		}
	}

	c.finalizeProxyMode()
	return c.finalizeUsers()
}

func (c *Config) normalizeFields() {
	c.Mode = normalizeLower(c.Mode)
	c.ServerAddress = strings.TrimSpace(c.ServerAddress)
	c.FallbackAddr = strings.TrimSpace(c.FallbackAddr)
	c.Key = strings.TrimSpace(c.Key)
	c.AEAD = normalizeLower(c.AEAD)
	c.CustomTable = strings.TrimSpace(c.CustomTable)
	c.CustomTables = trimNonEmpty(c.CustomTables)
	c.RuleURLs = trimNonEmpty(c.RuleURLs)
	c.SuspiciousAction = normalizeSuspiciousAction(c.SuspiciousAction)
	c.ASCII = normalizeASCII(c.ASCII)

	c.Transport = normalizeLower(c.Transport)
	if c.Transport == "" {
		c.Transport = "tcp"
	}
}

// finalizeHTTPMask keeps the top-level and httpmask.multiplex copies of the multiplex setting in sync.
func (c *Config) finalizeHTTPMask() {
	c.HTTPMask.Mode = normalizeHTTPMaskMode(c.HTTPMask.Mode)
	c.HTTPMask.Host = strings.TrimSpace(c.HTTPMask.Host)
	c.HTTPMask.PathRoot = strings.TrimSpace(c.HTTPMask.PathRoot)

	mux := c.MultiplexMode()
	c.Multiplex = mux
	c.HTTPMask.Multiplex = mux
}

// finalizeProxyMode derives ProxyMode from rule_urls: a lone "global"/"direct" keyword overrides
// ProxyMode and clears the list, and any other actionable rule source means PAC.
func (c *Config) finalizeProxyMode() {
	if len(c.RuleURLs) == 1 {
		if keyword := normalizeLower(c.RuleURLs[0]); keyword == "global" || keyword == "direct" {
			c.ProxyMode = keyword
			c.RuleURLs = nil
		}
	}
	if len(c.RuleURLs) > 0 && hasActionableRuleURLs(c.RuleURLs) {
		c.ProxyMode = "pac"
		return
	}
	c.ProxyMode = normalizeProxyMode(c.ProxyMode)
}
