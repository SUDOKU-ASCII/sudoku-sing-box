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

		sConn, _, err := HandshakeAndUpgradeWithTablesMeta(raw, &serverCfg, []*sudoku.Table{table})
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
