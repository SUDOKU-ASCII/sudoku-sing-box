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
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const envOutboundIface = "SUDOKU_OUTBOUND_IFACE"
const envOutboundSrcIP = "SUDOKU_OUTBOUND_SRC_IP"

var (
	darwinOutboundOnce sync.Once
	darwinOutboundIf   int
	darwinOutboundSrc4 *[4]byte
	darwinOutboundSrc6 *[16]byte
)

func darwinOutboundInterfaceIndex() int {
	darwinOutboundOnce.Do(func() {
		src := strings.TrimSpace(os.Getenv(envOutboundSrcIP))
		if src != "" {
			if ip := net.ParseIP(src); ip != nil && !ip.IsLoopback() {
				if ip4 := ip.To4(); ip4 != nil {
					var b [4]byte
					copy(b[:], ip4)
					darwinOutboundSrc4 = &b
				} else if ip16 := ip.To16(); ip16 != nil {
					var b [16]byte
					copy(b[:], ip16)
					darwinOutboundSrc6 = &b
				}
			}
		}

		name := strings.TrimSpace(os.Getenv(envOutboundIface))
		if name == "" {
			return
		}
		ifi, err := net.InterfaceByName(name)
		if err != nil || ifi == nil || ifi.Index <= 0 {
			return
		}
		darwinOutboundIf = ifi.Index
	})
	return darwinOutboundIf
}

func platformOutboundControl() func(network, address string, c syscall.RawConn) error {
	ifIndex := darwinOutboundInterfaceIndex()
	src4 := darwinOutboundSrc4
	src6 := darwinOutboundSrc6
	if ifIndex <= 0 && src4 == nil && src6 == nil {
		return nil
	}

	return func(network, address string, c syscall.RawConn) error {
		var inner error
		if err := c.Control(func(fd uintptr) {
			fdInt := int(fd)
			isV6 := strings.HasSuffix(network, "6")
			if !isV6 {
				host := address
				if h, _, err := net.SplitHostPort(address); err == nil && strings.TrimSpace(h) != "" {
					host = h
				}
				host = strings.TrimPrefix(host, "[")
				host = strings.TrimSuffix(host, "]")
				if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
					isV6 = true
				} else if strings.Count(host, ":") > 1 {
					isV6 = true
				}
			}

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
