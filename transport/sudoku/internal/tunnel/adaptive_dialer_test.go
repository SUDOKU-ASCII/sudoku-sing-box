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
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

type rawMuxTestSetup struct {
	server   config.Config
	client   config.Config
	table    *sudoku.Table
	listener net.Listener
}

func newRawMuxTestSetup(t *testing.T, withServer bool) *rawMuxTestSetup {
	t.Helper()

	base := config.Config{
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
	setup := &rawMuxTestSetup{
		server: base,
		client: base,
		table:  sudoku.NewTable(base.Key, base.ASCII),
	}
	setup.server.Mode = "server"
	setup.client.Mode = "client"

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	setup.listener = listener
	setup.client.ServerAddress = listener.Addr().String()

	if withServer {
		if err := setup.server.Finalize(); err != nil {
			t.Fatalf("finalize server config: %v", err)
		}
	}
	if err := setup.client.Finalize(); err != nil {
		t.Fatalf("finalize client config: %v", err)
	}
	return setup
}

func newRawMuxDialer(setup *rawMuxTestSetup) *MuxDialer {
	return &MuxDialer{BaseDialer: BaseDialer{
		Config: &setup.client,
		Tables: []*sudoku.Table{setup.table},
	}}
}

func echoTargetDialer(expectedTarget string) func(string) (net.Conn, error) {
	return func(target string) (net.Conn, error) {
		if expectedTarget != "" && target != expectedTarget {
			return nil, fmt.Errorf("unexpected target: %s", target)
		}
		targetConn, appConn := net.Pipe()
		go func() {
			defer appConn.Close()
			_, _ = io.Copy(appConn, appConn)
		}()
		return targetConn, nil
	}
}

func TestSudokuTunnel_StandardDialer(t *testing.T) {
	cfg := &config.Config{
		Mode:               "server",
		Transport:          "tcp",
		ServerAddress:      "127.0.0.1:0",
		Key:                "test-key-123",
		AEAD:               "chacha20-poly1305",
		PaddingMin:         0,
		PaddingMax:         0,
		ASCII:              "prefer_entropy",
		EnablePureDownlink: true,
		HTTPMask: config.HTTPMaskConfig{
			Disable:   false,
			Mode:      "legacy",
			Multiplex: "on",
		},
	}
	table := sudoku.NewTable(cfg.Key, cfg.ASCII)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	cfg.ServerAddress = listener.Addr().String()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				sConn, _, err := HandshakeAndUpgradeWithTablesMeta(c, cfg, []*sudoku.Table{table})
				if err != nil {
					return
				}
				defer sConn.Close()

				target, _, _, err := protocol.ReadAddress(sConn)
				if err != nil || target == "" {
					return
				}

				io.Copy(sConn, sConn)
			}(conn)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	dialer := &StandardDialer{
		BaseDialer: BaseDialer{
			Config: cfg,
			Tables: []*sudoku.Table{table},
		},
	}

	conn, err := dialer.Dial("example.com:80")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	message := "hello"
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, len(message))
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.ReadFull(conn, buf)
	}()
	_, _ = conn.Write([]byte(message))
	wg.Wait()
}

func TestSudokuTunnel_MuxDialerRawTCP(t *testing.T) {
	setup := newRawMuxTestSetup(t, true)
	if !setup.client.SessionMuxEnabled() || setup.client.HTTPMaskTunnelEnabled() {
		t.Fatalf("unexpected mux/httpmask state: mux=%v httpmask=%v", setup.client.SessionMuxEnabled(), setup.client.HTTPMaskTunnelEnabled())
	}

	serverErr := make(chan error, 1)
	go func() {
		raw, err := setup.listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer raw.Close()

		sConn, _, err := HandshakeAndUpgradeWithTablesMeta(raw, &setup.server, []*sudoku.Table{setup.table})
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

		serverErr <- HandleMuxWithDialer(sConn, nil, echoTargetDialer("example.com:80"))
	}()

	dialer := newRawMuxDialer(setup)
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
	setup := newRawMuxTestSetup(t, true)

	started := make(chan int, 2)
	serverErr := make(chan error, 1)
	go func() {
		for i := 1; i <= 2; i++ {
			raw, err := setup.listener.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			sConn, _, err := HandshakeAndUpgradeWithTablesMeta(raw, &setup.server, []*sudoku.Table{setup.table})
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
			serverErr <- HandleMuxWithDialer(sConn, nil, echoTargetDialer(""))
			return
		}
	}()

	dialer := newRawMuxDialer(setup)
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
	setup := newRawMuxTestSetup(t, false)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := setup.listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	dialer := newRawMuxDialer(setup)
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
