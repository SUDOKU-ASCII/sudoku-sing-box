package sudoku_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"

	"github.com/stretchr/testify/require"
)

func TestSudoku_OfficialInterop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping interop test in -short mode")
	}
	if os.Getenv("SUDOKU_OFFICIAL_INTEROP") != "1" {
		t.Skip("set SUDOKU_OFFICIAL_INTEROP=1 to enable")
	}

	officialBin := os.Getenv("SUDOKU_OFFICIAL_BIN")
	if officialBin == "" {
		path, err := exec.LookPath("sudoku")
		if err != nil {
			t.Skip("set SUDOKU_OFFICIAL_BIN or put `sudoku` in PATH")
		}
		officialBin = path
	}

	profiles := []struct {
		name         string
		customTables []string
	}{
		{name: "default"},
		{name: "custom_tables", customTables: []string{"xpxvvpvv", "vxpvxvvp", "vpxpvvxv"}},
	}

	for _, aead := range []string{"aes-128-gcm", "chacha20-poly1305"} {
		aead := aead
		for _, profile := range profiles {
			profile := profile
			t.Run(aead+"/"+profile.name, func(t *testing.T) {
				t.Run("official_server-singbox_outbound", func(t *testing.T) {
					runOfficialServerSingBoxOutbound(t, officialBin, aead, profile.customTables, false)
					if profile.name == "default" {
						runOfficialServerSingBoxOutbound(t, officialBin, aead, profile.customTables, true)
					}
				})
				t.Run("official_client-singbox_inbound", func(t *testing.T) {
					runOfficialClientSingBoxInbound(t, officialBin, aead, profile.customTables, false)
					if profile.name == "default" {
						runOfficialClientSingBoxInbound(t, officialBin, aead, profile.customTables, true)
					}
				})
				t.Run("singbox-singbox", func(t *testing.T) {
					runSingBoxToSingBox(t, aead, profile.customTables, false)
					if profile.name == "default" {
						runSingBoxToSingBox(t, aead, profile.customTables, true)
					}
				})
			})
		}
	}
}

func runOfficialServerSingBoxOutbound(t *testing.T, officialBin string, aead string, customTables []string, mux bool) {
	key := "test_key_interop"
	serverPort := allocateTCPPort(t)
	clientPort := allocateTCPPort(t)

	officialCfg := map[string]any{
		"mode":                 "server",
		"local_port":           int(serverPort),
		"server_address":       "127.0.0.1:1",
		"fallback_address":     "127.0.0.1:80",
		"key":                  key,
		"aead":                 aead,
		"padding_min":          1,
		"padding_max":          9,
		"ascii":                "prefer_entropy",
		"custom_tables":        customTables,
		"enable_pure_downlink": false,
		"disable_http_mask":    !mux,
		"http_mask_mode":       ternary(mux, "stream", "legacy"),
		"http_mask_multiplex":  ternary(mux, "on", "off"),
		"http_mask_tls":        false,
	}
	officialLog := startOfficial(t, officialBin, officialCfg)
	waitTCPPort(t, serverPort, "official server")

	singBoxOpts := option.Options{
		Log: &option.LogOptions{Level: "warning"},
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
				Tag:  "direct",
			},
			{
				Type: C.TypeSudoku,
				Tag:  "sudoku-out",
				Options: &option.SudokuOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Key:                key,
					AEADMethod:         aead,
					PaddingMin:         ptr(1),
					PaddingMax:         ptr(9),
					ASCII:              "prefer_entropy",
					CustomTables:       customTables,
					EnablePureDownlink: ptr(false),
					DisableHTTPMask:    !mux,
					HTTPMaskMode:       ternary(mux, "stream", "legacy"),
					HTTPMaskTLS:        false,
					HTTPMaskMultiplex:  ternary(mux, "on", "off"),
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "sudoku-out",
							},
						},
					},
				},
			},
		},
	}
	startBox(t, singBoxOpts)

	runSuite(t, clientPort, mux)

	_ = officialLog
}

func runOfficialClientSingBoxInbound(t *testing.T, officialBin string, aead string, customTables []string, mux bool) {
	key := "test_key_interop"
	serverPort := allocateTCPPort(t)
	clientPort := allocateTCPPort(t)

	singBoxOpts := option.Options{
		Log: &option.LogOptions{Level: "warning"},
		Inbounds: []option.Inbound{
			{
				Type: C.TypeSudoku,
				Tag:  "sudoku-in",
				Options: &option.SudokuInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Key:               key,
					AEADMethod:        aead,
					PaddingMin:        ptr(1),
					PaddingMax:        ptr(9),
					ASCII:             "prefer_entropy",
					CustomTables:      customTables,
					EnablePureDownlink: ptr(false),
					DisableHTTPMask:    !mux,
					HTTPMaskMode:       ternary(mux, "stream", "legacy"),
					HTTPMaskMultiplex:  ternary(mux, "on", "off"),
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
				Tag:  "direct",
			},
		},
		Route: &option.RouteOptions{
			Final: "direct",
		},
	}
	startBox(t, singBoxOpts)
	waitTCPPort(t, serverPort, "sing-box inbound")

	officialCfg := map[string]any{
		"mode":                 "client",
		"local_port":           int(clientPort),
		"server_address":       fmt.Sprintf("127.0.0.1:%d", serverPort),
		"fallback_address":     "127.0.0.1:80",
		"key":                  key,
		"aead":                 aead,
		"padding_min":          1,
		"padding_max":          9,
		"ascii":                "prefer_entropy",
		"custom_tables":        customTables,
		"enable_pure_downlink": false,
		"disable_http_mask":    !mux,
		"http_mask_mode":       ternary(mux, "stream", "legacy"),
		"http_mask_multiplex":  ternary(mux, "on", "off"),
		"http_mask_tls":        false,
		"rule_urls":            []string{"global"},
	}
	officialLog := startOfficial(t, officialBin, officialCfg)
	waitTCPPort(t, clientPort, "official client")

	runSuite(t, clientPort, mux)

	_ = officialLog
}

func runSingBoxToSingBox(t *testing.T, aead string, customTables []string, mux bool) {
	key := "test_key_interop"
	serverPort := allocateTCPPort(t)
	clientPort := allocateTCPPort(t)

	serverOpts := option.Options{
		Log: &option.LogOptions{Level: "warning"},
		Inbounds: []option.Inbound{
			{
				Type: C.TypeSudoku,
				Tag:  "sudoku-in",
				Options: &option.SudokuInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Key:                key,
					AEADMethod:         aead,
					PaddingMin:         ptr(1),
					PaddingMax:         ptr(9),
					ASCII:              "prefer_entropy",
					CustomTables:       customTables,
					EnablePureDownlink: ptr(false),
					DisableHTTPMask:    !mux,
					HTTPMaskMode:       ternary(mux, "stream", "legacy"),
					HTTPMaskMultiplex:  ternary(mux, "on", "off"),
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
				Tag:  "direct",
			},
		},
		Route: &option.RouteOptions{Final: "direct"},
	}
	startBox(t, serverOpts)
	waitTCPPort(t, serverPort, "sing-box server")

	clientOpts := option.Options{
		Log: &option.LogOptions{Level: "warning"},
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
				Tag:  "direct",
			},
			{
				Type: C.TypeSudoku,
				Tag:  "sudoku-out",
				Options: &option.SudokuOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Key:                key,
					AEADMethod:         aead,
					PaddingMin:         ptr(1),
					PaddingMax:         ptr(9),
					ASCII:              "prefer_entropy",
					CustomTables:       customTables,
					EnablePureDownlink: ptr(false),
					DisableHTTPMask:    !mux,
					HTTPMaskMode:       ternary(mux, "stream", "legacy"),
					HTTPMaskTLS:        false,
					HTTPMaskMultiplex:  ternary(mux, "on", "off"),
				},
			},
		},
		Route: &option.RouteOptions{
			Final: "sudoku-out",
		},
	}
	startBox(t, clientOpts)

	runSuite(t, clientPort, mux)
}

func runSuite(t *testing.T, clientPort uint16, mux bool) {
	t.Run("tcp_udp_ping_pong", func(t *testing.T) {
		testPort := allocateTCPPort(t)
		dialTCP, dialUDP := dialViaSocks(t, clientPort, testPort)

		runPingPongTCP(t, testPort, dialTCP)
		runPingPongUDP(t, testPort, dialUDP)
	})

	t.Run("large_data", func(t *testing.T) {
		testPort := allocateTCPPort(t)
		dialTCP, dialUDP := dialViaSocks(t, clientPort, testPort)

		runLargeDataTCP(t, testPort, dialTCP)
		runLargeDataUDP(t, testPort, dialUDP)
	})

	if mux {
		t.Run("multi_load_tcp", func(t *testing.T) {
			ports := make([]uint16, 8)
			dialers := make([]func() (net.Conn, error), 8)
			for i := range ports {
				ports[i] = allocateTCPPort(t)
				dialTCP, _ := dialViaSocks(t, clientPort, ports[i])
				dialers[i] = dialTCP
			}

			var group sync.WaitGroup
			errCh := make(chan error, 8)
			for i := range ports {
				port := ports[i]
				dialTCP := dialers[i]
				group.Add(1)
				go func() {
					defer group.Done()
					if err := runLargeDataTCPNoT(t, port, dialTCP); err != nil {
						errCh <- err
					}
				}()
			}
			group.Wait()
			close(errCh)
			for err := range errCh {
				require.NoError(t, err)
			}
		})
	}
}

func dialViaSocks(t *testing.T, clientPort uint16, testPort uint16) (func() (net.Conn, error), func() (net.PacketConn, error)) {
	t.Helper()
	dialer := socks.NewClient(N.SystemDialer, M.ParseSocksaddrHostPort("127.0.0.1", clientPort), socks.Version5, "", "")
	dialTCP := func() (net.Conn, error) {
		return dialer.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("127.0.0.1", testPort))
	}
	dialUDP := func() (net.PacketConn, error) {
		return dialer.ListenPacket(context.Background(), M.ParseSocksaddrHostPort("127.0.0.1", testPort))
	}
	return dialTCP, dialUDP
}

func runPingPongTCP(t *testing.T, port uint16, dial func() (net.Conn, error)) {
	t.Helper()
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var buf [4]byte
		_, _ = io.ReadFull(conn, buf[:])
		_, _ = conn.Write([]byte("pong"))
	}()

	c, err := dial()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	_, err = c.Write([]byte("ping"))
	require.NoError(t, err)
	var resp [4]byte
	_, err = io.ReadFull(c, resp[:])
	require.NoError(t, err)
	require.Equal(t, "pong", string(resp[:]))
	<-done
}

func runPingPongUDP(t *testing.T, port uint16, dial func() (net.PacketConn, error)) {
	t.Helper()
	pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [1024]byte
		n, addr, err := pc.ReadFrom(buf[:])
		if err != nil {
			return
		}
		_ = n
		_, _ = pc.WriteTo([]byte("pong"), addr)
	}()

	c, err := dial()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	rAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}
	_, err = c.WriteTo([]byte("ping"), rAddr)
	require.NoError(t, err)
	var resp [1024]byte
	n, _, err := c.ReadFrom(resp[:])
	require.NoError(t, err)
	require.Equal(t, "pong", string(resp[:n]))
	<-done
}

func runLargeDataTCP(t *testing.T, port uint16, dial func() (net.Conn, error)) {
	t.Helper()
	require.NoError(t, runLargeDataTCPNoT(t, port, dial))
}

func runLargeDataTCPNoT(t *testing.T, port uint16, dial func() (net.Conn, error)) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer l.Close()

	const times = 50
	const chunkSize = 64 * 1024

	type hashPair struct {
		send map[int][16]byte
		recv map[int][16]byte
	}

	ch := make(chan hashPair, 2)
	writeRand := func(w io.Writer) (map[int][16]byte, error) {
		hashMap := make(map[int][16]byte, times)
		buf := make([]byte, chunkSize)
		for i := 0; i < times; i++ {
			if _, err := rand.Read(buf[1:]); err != nil {
				return nil, err
			}
			buf[0] = byte(i)
			hashMap[i] = md5.Sum(buf)
			if _, err := w.Write(buf); err != nil {
				return nil, err
			}
		}
		return hashMap, nil
	}
	readRand := func(r io.Reader) (map[int][16]byte, error) {
		hashMap := make(map[int][16]byte, times)
		buf := make([]byte, chunkSize)
		for i := 0; i < times; i++ {
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, err
			}
			hashMap[int(buf[0])] = md5.Sum(buf)
		}
		return hashMap, nil
	}

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		recv, err := readRand(conn)
		if err != nil {
			return
		}
		send, err := writeRand(conn)
		if err != nil {
			return
		}
		ch <- hashPair{send: send, recv: recv}
	}()

	conn, err := dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	send, err := writeRand(conn)
	if err != nil {
		return err
	}
	recv, err := readRand(conn)
	if err != nil {
		return err
	}
	ch <- hashPair{send: send, recv: recv}

	a := <-ch
	b := <-ch

	// A<->B should match.
	for i := 0; i < times; i++ {
		if a.send[i] != b.recv[i] || b.send[i] != a.recv[i] {
			return E.New("hash mismatch")
		}
	}
	return nil
}

func runLargeDataUDP(t *testing.T, port uint16, dial func() (net.PacketConn, error)) {
	t.Helper()
	pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })

	const times = 50
	const chunkSize = 1500

	type hashPair struct {
		send map[int][16]byte
		recv map[int][16]byte
	}

	ch := make(chan hashPair, 2)

	go func() {
		var peer net.Addr
		recv := make(map[int][16]byte, times)
		buf := make([]byte, 64*1024)
		for i := 0; i < times; i++ {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			peer = addr
			if n < 1 {
				return
			}
			recv[int(buf[0])] = md5.Sum(buf[:chunkSize])
		}

		send := make(map[int][16]byte, times)
		for i := 0; i < times; i++ {
			b := make([]byte, chunkSize)
			if _, err := rand.Read(b[1:]); err != nil {
				return
			}
			b[0] = byte(i)
			send[i] = md5.Sum(b)
			if _, err := pc.WriteTo(b, peer); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		ch <- hashPair{send: send, recv: recv}
	}()

	c, err := dial()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))

	rAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}

	send := make(map[int][16]byte, times)
	for i := 0; i < times; i++ {
		b := make([]byte, chunkSize)
		if _, err := rand.Read(b[1:]); err != nil {
			t.Fatal(err)
		}
		b[0] = byte(i)
		send[i] = md5.Sum(b)
		_, err := c.WriteTo(b, rAddr)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond)
	}

	recv := make(map[int][16]byte, times)
	buf := make([]byte, 64*1024)
	for i := 0; i < times; i++ {
		_, _, err := c.ReadFrom(buf)
		require.NoError(t, err)
		recv[int(buf[0])] = md5.Sum(buf[:chunkSize])
	}
	ch <- hashPair{send: send, recv: recv}

	a := <-ch
	b := <-ch
	for i := 0; i < times; i++ {
		require.Equal(t, a.send[i], b.recv[i])
		require.Equal(t, b.send[i], a.recv[i])
	}
}

func startBox(t *testing.T, options option.Options) *box.Box {
	t.Helper()
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, err)
	require.NoError(t, instance.Start())
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
	return instance
}

func startOfficial(t *testing.T, officialBin string, cfg map[string]any) *bytes.Buffer {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	content, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, content, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, officialBin, "-c", cfgPath)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	return &buf
}

func waitTCPPort(t *testing.T, port uint16, name string) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "%s not ready on %s", name, addr)
}

func allocateTCPPort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	require.NoError(t, l.Close())
	return port
}

func ptr[T any](v T) *T { return &v }

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
