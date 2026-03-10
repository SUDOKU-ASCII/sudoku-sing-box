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
package dnsutil

import (
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	envOutboundDisable = "SUDOKU_OUTBOUND_DISABLE"
)

var (
	outboundOnce    sync.Once
	outboundControl func(network, address string, c syscall.RawConn) error
)

// OutboundDialer returns a net.Dialer that applies best-effort platform-specific
// outbound bypass options when the host process provides them via environment variables.
func OutboundDialer(timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if ctrl := outboundDialerControl(); ctrl != nil {
		d.Control = ctrl
	}
	return d
}

func outboundDialerControl() func(network, address string, c syscall.RawConn) error {
	outboundOnce.Do(func() {
		if strings.TrimSpace(os.Getenv(envOutboundDisable)) == "1" {
			return
		}
		base := platformOutboundControl()
		if base == nil {
			return
		}
		outboundControl = func(network, address string, c syscall.RawConn) error {
			if !shouldApplyOutboundControl(network, address) {
				return nil
			}
			return base(network, address, c)
		}
	})
	return outboundControl
}

func shouldApplyOutboundControl(network string, address string) bool {
	if strings.HasPrefix(network, "unix") {
		return false
	}

	host := address
	if h, _, err := net.SplitHostPort(address); err == nil && strings.TrimSpace(h) != "" {
		host = h
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return !isLikelyLocalHostname(host)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return true
}
