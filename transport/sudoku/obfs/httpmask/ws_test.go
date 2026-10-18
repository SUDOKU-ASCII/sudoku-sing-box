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
package httpmask

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestWebSocketTunnel_Echo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srv := NewTunnelServer(TunnelServerOptions{
		Mode:     "ws",
		PathRoot: "root",
		AuthKey:  "k",
	})

	errCh := make(chan error, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer raw.Close()

		res, c, err := srv.HandleConn(raw)
		if err != nil {
			errCh <- err
			return
		}
		if res != HandleStartTunnel || c == nil {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		defer c.Close()

		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 5)
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- err
			return
		}
		if _, err := c.Write(buf); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := DialTunnel(ctx, ln.Addr().String(), TunnelDialOptions{
		Mode:       "ws",
		TLSEnabled: false,
		PathRoot:   "root",
		AuthKey:    "k",
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	msg := []byte("hello")
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo mismatch: got %q want %q", string(buf), string(msg))
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("server timeout")
	}
}
