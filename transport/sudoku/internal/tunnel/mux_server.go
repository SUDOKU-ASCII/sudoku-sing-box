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
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
)

// HandleMuxServer handles a multiplexed tunnel connection after the control plane has selected mux mode.
func HandleMuxServer(conn net.Conn, onConnect func(targetAddr string)) error {
	dial := func(addr string) (net.Conn, error) {
		return net.DialTimeout("tcp", addr, 10*time.Second)
	}
	return HandleMuxWithDialer(conn, onConnect, dial)
}

func HandleMuxWithStreamHandler(conn net.Conn, handler func(stream net.Conn, targetAddr string)) error {
	if conn == nil {
		return fmt.Errorf("nil conn")
	}
	if handler == nil {
		return fmt.Errorf("nil handler")
	}

	sess := newMuxSession(conn, func(stream *muxStream, payload []byte) {
		addr, err := decodeMuxOpenTarget(payload)
		if err != nil {
			stream.session.sendReset(stream.id, "bad address")
			stream.closeNoSend(err)
			stream.session.removeStream(stream.id)
			return
		}
		go handler(stream, addr)
	})
	sess.startKeepalive(muxKeepaliveInterval)

	<-sess.closed
	err := sess.closedErr()
	if isExpectedMuxSessionClose(err) {
		return nil
	}
	return err
}

// HandleMuxWithDialer is like HandleMuxServer but allows the caller to control how targets are dialed.
//
// The dialTarget callback must establish a TCP connection to targetAddr (host:port). It may enforce
// allow-lists (useful for reverse proxy clients).
func HandleMuxWithDialer(conn net.Conn, onConnect func(targetAddr string), dialTarget func(targetAddr string) (net.Conn, error)) error {
	if conn == nil {
		return fmt.Errorf("nil conn")
	}
	if dialTarget == nil {
		return fmt.Errorf("nil dialTarget")
	}

	sess := newMuxSession(conn, func(stream *muxStream, payload []byte) {
		sess := stream.session
		addr, err := decodeMuxOpenTarget(payload)
		if err != nil {
			sess.sendReset(stream.id, "bad address")
			stream.closeNoSend(err)
			sess.removeStream(stream.id)
			return
		}
		if onConnect != nil {
			onConnect(addr)
		}

		target, err := dialTarget(addr)
		if err != nil {
			sess.sendReset(stream.id, err.Error())
			stream.closeNoSend(err)
			sess.removeStream(stream.id)
			return
		}

		connutil.PipeConn(stream, target)
	})
	sess.startKeepalive(muxKeepaliveInterval)

	<-sess.closed
	err := sess.closedErr()
	if isExpectedMuxSessionClose(err) {
		return nil
	}
	return err
}

func isExpectedMuxSessionClose(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset by peer") || strings.Contains(msg, "forcibly closed by the remote host")
}

func decodeMuxOpenTarget(payload []byte) (string, error) {
	addr, _, _, err := protocol.ReadAddress(bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	if addr == "" {
		return "", fmt.Errorf("empty address")
	}
	return addr, nil
}
