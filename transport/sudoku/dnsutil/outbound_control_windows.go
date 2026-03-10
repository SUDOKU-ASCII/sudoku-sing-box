//go:build windows

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
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const envOutboundIfIndex = "SUDOKU_OUTBOUND_IFINDEX"

const (
	ipUnicastIf   = 31
	ipv6UnicastIf = 31
)

func platformOutboundControl() func(network, address string, c syscall.RawConn) error {
	raw := strings.TrimSpace(os.Getenv(envOutboundIfIndex))
	if raw == "" {
		return nil
	}
	ifIndex, err := strconv.Atoi(raw)
	if err != nil || ifIndex <= 0 {
		return nil
	}

	return func(network, address string, c syscall.RawConn) error {
		var inner error
		if err := c.Control(func(fd uintptr) {
			handle := syscall.Handle(fd)
			err4 := outboundBind4(handle, ifIndex)
			err6 := outboundBind6(handle, ifIndex)
			if err4 != nil && err6 != nil {
				if strings.HasSuffix(network, "6") || strings.Contains(address, ":") {
					inner = err6
				} else {
					inner = err4
				}
				return
			}
			inner = nil
		}); err != nil {
			return err
		}
		return inner
	}
}

func outboundBind4(handle syscall.Handle, ifaceIdx int) error {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], uint32(ifaceIdx))
	idx := *(*uint32)(unsafe.Pointer(&bytes[0]))
	return syscall.SetsockoptInt(handle, syscall.IPPROTO_IP, ipUnicastIf, int(idx))
}

func outboundBind6(handle syscall.Handle, ifaceIdx int) error {
	return syscall.SetsockoptInt(handle, syscall.IPPROTO_IPV6, ipv6UnicastIf, ifaceIdx)
}
