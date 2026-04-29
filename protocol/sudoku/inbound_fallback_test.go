package sudoku_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestSudokuInboundFallback_HTTPMaskRejected(t *testing.T) {
	fallbackLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = fallbackLn.Close() })

	const body = "fallback-body-once"
	requestCh := make(chan []byte, 1)
	go func() {
		conn, err := fallbackLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		var req bytes.Buffer
		buf := make([]byte, 1024)
		for !bytes.Contains(req.Bytes(), []byte("\r\n\r\n"+body)) {
			n, err := conn.Read(buf)
			if n > 0 {
				_, _ = req.Write(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					requestCh <- req.Bytes()
				}
				return
			}
		}
		requestCh <- req.Bytes()
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 11\r\n\r\nfallback ok"))
	}()

	serverPort := allocateTCPPort(t)
	startBox(t, option.Options{
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
					Key:              "test-key-fallback",
					FallbackAddress:  fallbackLn.Addr().String(),
					SuspiciousAction: "fallback",
					HTTPMaskMode:     "auto",
				},
			},
		},
		Outbounds: []option.Outbound{{Type: C.TypeDirect, Tag: "direct"}},
		Route:     &option.RouteOptions{Final: "direct"},
	})
	waitTCPPort(t, serverPort, "sudoku inbound")

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", serverPort), 3*time.Second)
	require.NoError(t, err)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "POST /session?token=bad HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"X-Sudoku-Tunnel: stream\r\n" +
		"Authorization: Bearer wrong\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)) +
		body
	_, err = io.WriteString(conn, req)
	require.NoError(t, err)

	resp := make([]byte, 256)
	n, err := conn.Read(resp)
	require.NoError(t, err)
	require.Contains(t, string(resp[:n]), "fallback ok")

	select {
	case got := <-requestCh:
		gotText := string(got)
		require.Contains(t, gotText, "X-Sudoku-Tunnel: stream")
		require.Equal(t, 1, strings.Count(gotText, body), "fallback replay duplicated or lost request body")
	case <-time.After(5 * time.Second):
		t.Fatal("fallback server did not receive rejected httpmask request")
	}
}
