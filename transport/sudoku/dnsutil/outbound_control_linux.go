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
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const envOutboundMark = "SUDOKU_OUTBOUND_MARK"
const envOutboundSrcIP = "SUDOKU_OUTBOUND_SRC_IP"

func platformOutboundControl() func(network, address string, c syscall.RawConn) error {
	raw := strings.TrimSpace(os.Getenv(envOutboundMark))
	mark := 0
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			mark = v
		}
	}

	src := strings.TrimSpace(os.Getenv(envOutboundSrcIP))
	var src4 *[4]byte
	var src6 *[16]byte
	if src != "" {
		if ip := net.ParseIP(src); ip != nil && !ip.IsLoopback() {
			if ip4 := ip.To4(); ip4 != nil {
				var b [4]byte
				copy(b[:], ip4)
				src4 = &b
			} else if ip16 := ip.To16(); ip16 != nil {
				var b [16]byte
				copy(b[:], ip16)
				src6 = &b
			}
		}
	}

	if mark <= 0 && src4 == nil && src6 == nil {
		return nil
	}

	return func(network string, address string, c syscall.RawConn) error {
		var inner error
		if err := c.Control(func(fd uintptr) {
			fdInt := int(fd)
			if src4 != nil && !strings.HasSuffix(network, "6") {
				if berr := unix.Bind(fdInt, &unix.SockaddrInet4{Addr: *src4}); berr != nil {
					inner = berr
					return
				}
			} else if src6 != nil {
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
