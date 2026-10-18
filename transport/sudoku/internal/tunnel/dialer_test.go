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
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func TestSudokuTunnel_MuxDialerRawTCP(t *testing.T) {
	baseCfg := config.Config{
		Transport:          "tcp",
		Key:                "test-key-123",
		AEAD:               "chacha20-poly1305",
		PaddingMin:         0,
		PaddingMax:         0,
		ASCII:              "prefer_entropy",
		EnablePureDownlink: true,
		Multiplex:          "on",
		HTTPMask: config.HTTPMaskConfig{
			Disable: true,
			Mode:    "legacy",
		},
	}
	serverCfg := baseCfg
	serverCfg.Mode = "server"
	clientCfg := baseCfg
	clientCfg.Mode = "client"

	table := sudoku.NewTable(baseCfg.Key, baseCfg.ASCII)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	clientCfg.ServerAddress = listener.Addr().String()
	if err := serverCfg.Finalize(); err != nil {
		t.Fatalf("finalize server config: %v", err)
	}
	if err := clientCfg.Finalize(); err != nil {
		t.Fatalf("finalize client config: %v", err)
	}
	if !clientCfg.SessionMuxEnabled() || clientCfg.HTTPMaskTunnelEnabled() {
		t.Fatalf("unexpected mux/httpmask state: mux=%v httpmask=%v", clientCfg.SessionMuxEnabled(), clientCfg.HTTPMaskTunnelEnabled())
	}

	serverErr := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer raw.Close()

		sConn, _, err := ServerHandshake(raw, &serverCfg, []*sudoku.Table{table})
		if err != nil {
			serverErr <- err
			return
		}

		msg, err := ReadKIPMessage(sConn)
		if err != nil {
			serverErr <- err
			return
		}
		if msg.Type != KIPTypeStartMux {
			serverErr <- fmt.Errorf("unexpected first message: %d", msg.Type)
			return
		}

		serverErr <- HandleMuxWithDialer(sConn, nil, func(targetAddr string) (net.Conn, error) {
			if targetAddr != "example.com:80" {
				return nil, fmt.Errorf("unexpected target: %s", targetAddr)
			}
			targetConn, appConn := net.Pipe()
			go func() {
				defer appConn.Close()
				_, _ = io.Copy(appConn, appConn)
			}()
			return targetConn, nil
		})
	}()

	dialer := &MuxDialer{BaseDialer: BaseDialer{
		Config: &clientCfg,
		Tables: []*sudoku.Table{table},
	}}
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer warmCancel()
	if err := dialer.Warm(warmCtx); err != nil {
		t.Fatalf("warm: %v", err)
	}
	conn, err := dialer.Dial("example.com:80")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	message := []byte("hello raw mux")
	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, len(message))
		if _, err := io.ReadFull(conn, buf); err != nil {
			readErr <- err
			return
		}
		if string(buf) != string(message) {
			readErr <- fmt.Errorf("echo mismatch: got %q want %q", buf, message)
			return
		}
		readErr <- nil
	}()
	if _, err := conn.Write(message); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("read timed out")
	}
	_ = conn.Close()

	dialer.mu.Lock()
	if dialer.session != nil {
		dialer.session.closeWithError(io.ErrClosedPipe)
	}
	dialer.mu.Unlock()

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("server did not exit")
	}
}

func TestMuxDialer_MaintainReconnectsClosedSession(t *testing.T) {
	baseCfg := config.Config{
		Transport:          "tcp",
		Key:                "test-key-123",
		AEAD:               "chacha20-poly1305",
		ASCII:              "prefer_entropy",
		EnablePureDownlink: true,
		Multiplex:          "on",
		HTTPMask: config.HTTPMaskConfig{
			Disable: true,
		},
	}
	serverCfg := baseCfg
	serverCfg.Mode = "server"
	clientCfg := baseCfg
	clientCfg.Mode = "client"

	table := sudoku.NewTable(baseCfg.Key, baseCfg.ASCII)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	clientCfg.ServerAddress = listener.Addr().String()
	if err := serverCfg.Finalize(); err != nil {
		t.Fatalf("finalize server config: %v", err)
	}
	if err := clientCfg.Finalize(); err != nil {
		t.Fatalf("finalize client config: %v", err)
	}

	started := make(chan int, 2)
	serverErr := make(chan error, 1)
	go func() {
		for i := 1; i <= 2; i++ {
			raw, err := listener.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			sConn, _, err := ServerHandshake(raw, &serverCfg, []*sudoku.Table{table})
			if err != nil {
				_ = raw.Close()
				serverErr <- err
				return
			}
			msg, err := ReadKIPMessage(sConn)
			if err != nil {
				_ = sConn.Close()
				serverErr <- err
				return
			}
			if msg.Type != KIPTypeStartMux {
				_ = sConn.Close()
				serverErr <- fmt.Errorf("unexpected first message: %d", msg.Type)
				return
			}
			started <- i
			if i == 1 {
				_ = sConn.Close()
				continue
			}
			serverErr <- HandleMuxWithDialer(sConn, nil, func(string) (net.Conn, error) {
				targetConn, appConn := net.Pipe()
				go func() {
					defer appConn.Close()
					_, _ = io.Copy(appConn, appConn)
				}()
				return targetConn, nil
			})
			return
		}
	}()

	dialer := &MuxDialer{BaseDialer: BaseDialer{
		Config: &clientCfg,
		Tables: []*sudoku.Table{table},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	maintainDone := make(chan struct{})
	go func() {
		dialer.Maintain(ctx, nil)
		close(maintainDone)
	}()

	for want := 1; want <= 2; want++ {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("session start = %d, want %d", got, want)
			}
		case err := <-serverErr:
			t.Fatalf("server before reconnect: %v", err)
		case <-ctx.Done():
			t.Fatalf("wait for session %d: %v", want, ctx.Err())
		}
	}

	conn, err := dialer.Dial("example.com:80")
	if err != nil {
		t.Fatalf("dial after reconnect: %v", err)
	}
	payload := []byte("after reconnect")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write after reconnect: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read after reconnect: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}
	_ = conn.Close()
	_ = dialer.Close()
	cancel()
	select {
	case <-maintainDone:
	case <-time.After(time.Second):
		t.Fatal("maintainer did not stop")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not exit")
	}
}

func TestMuxDialer_CloseCancelsSessionCreation(t *testing.T) {
	baseCfg := config.Config{
		Transport:          "tcp",
		Key:                "test-key-123",
		AEAD:               "chacha20-poly1305",
		ASCII:              "prefer_entropy",
		EnablePureDownlink: true,
		Multiplex:          "on",
		HTTPMask: config.HTTPMaskConfig{
			Disable: true,
		},
	}
	clientCfg := baseCfg
	clientCfg.Mode = "client"

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	clientCfg.ServerAddress = listener.Addr().String()
	if err := clientCfg.Finalize(); err != nil {
		t.Fatalf("finalize client config: %v", err)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	table := sudoku.NewTable(baseCfg.Key, baseCfg.ASCII)
	dialer := &MuxDialer{BaseDialer: BaseDialer{
		Config: &clientCfg,
		Tables: []*sudoku.Table{table},
	}}
	warmDone := make(chan error, 1)
	go func() {
		warmDone <- dialer.Warm(context.Background())
	}()

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
		defer serverConn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("session creation did not connect")
	}

	if err := dialer.Close(); err != nil {
		t.Fatalf("close dialer: %v", err)
	}
	select {
	case err := <-warmDone:
		if err == nil {
			t.Fatal("warm unexpectedly succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not cancel session creation")
	}
}
