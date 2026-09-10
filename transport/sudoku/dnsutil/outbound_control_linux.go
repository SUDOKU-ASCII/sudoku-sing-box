//go:build linux

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
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func platformOutboundControl() func(network, address string, c syscall.RawConn) error {
	raw := strings.TrimSpace(os.Getenv(envOutboundMark))
	mark := 0
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			mark = v
		}
	}

	src4, src6 := outboundSourceIPs()

	if mark <= 0 && src4 == nil && src6 == nil {
		return nil
	}

	return func(network string, address string, c syscall.RawConn) error {
		var inner error
		if err := c.Control(func(fd uintptr) {
			fdInt := int(fd)
			isV6 := outboundIPv6(network, address)
			if src4 != nil && !isV6 {
				if berr := unix.Bind(fdInt, &unix.SockaddrInet4{Addr: *src4}); berr != nil {
					inner = berr
					return
				}
			} else if src6 != nil && isV6 {
				if berr := unix.Bind(fdInt, &unix.SockaddrInet6{Addr: *src6}); berr != nil {
					inner = berr
					return
				}
			}

			if mark > 0 {
				merr := unix.SetsockoptInt(fdInt, unix.SOL_SOCKET, unix.SO_MARK, mark)
				if merr == unix.EPERM || merr == unix.EACCES {
					merr = nil
				}
				if inner == nil {
					inner = merr
				}
			}
		}); err != nil {
			return err
		}
		return inner
	}
}
