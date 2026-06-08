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
	"bytes"
	"fmt"
	"net"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
)

func writeKIPOpenTCP(conn net.Conn, addr string) error {
	if conn == nil {
		return fmt.Errorf("nil conn")
	}
	msg, err := encodeKIPOpenTCP(addr)
	if err != nil {
		return err
	}
	return connutil.WriteFull(conn, msg)
}

func encodeKIPOpenTCP(addr string) ([]byte, error) {
	var b bytes.Buffer
	if err := protocol.WriteAddress(&b, addr); err != nil {
		return nil, fmt.Errorf("encode address failed: %w", err)
	}
	var msg bytes.Buffer
	if err := WriteKIPMessage(&msg, KIPTypeOpenTCP, b.Bytes()); err != nil {
		return nil, err
	}
	return msg.Bytes(), nil
}
