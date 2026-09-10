//go:build darwin

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
	"syscall"

	"golang.org/x/sys/unix"
)

// The common dialer caches this closure; no second platform-level cache is needed.
func platformOutboundControl() func(network, address string, c syscall.RawConn) error {
	src4, src6 := outboundSourceIPs()
	ifIndex := 0
	if name := strings.TrimSpace(os.Getenv(envOutboundIface)); name != "" {
		if iface, err := net.InterfaceByName(name); err == nil && iface.Index > 0 {
			ifIndex = iface.Index
		}
	}
	if ifIndex <= 0 && src4 == nil && src6 == nil {
		return nil
	}

	return func(network, address string, c syscall.RawConn) error {
		var inner error
		if err := c.Control(func(fd uintptr) {
			fdInt := int(fd)
			isV6 := outboundIPv6(network, address)

			if ifIndex > 0 {
				var errBound error
				if isV6 {
					errBound = unix.SetsockoptInt(fdInt, unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, ifIndex)
				} else {
					errBound = unix.SetsockoptInt(fdInt, unix.IPPROTO_IP, unix.IP_BOUND_IF, ifIndex)
				}
				if errBound == nil {
					inner = nil
					return
				}
			}

			if !isV6 && src4 != nil {
				if berr := unix.Bind(fdInt, &unix.SockaddrInet4{Addr: *src4}); berr != nil {
					inner = berr
					return
				}
				inner = nil
				return
			}
			if isV6 && src6 != nil {
				if berr := unix.Bind(fdInt, &unix.SockaddrInet6{Addr: *src6}); berr != nil {
					inner = berr
					return
				}
				inner = nil
				return
			}

			inner = nil
		}); err != nil {
			return err
		}
		return inner
	}
}
