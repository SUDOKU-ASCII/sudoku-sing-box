package sudoku_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSudoku_ReverseProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/hello", r.URL.Path)
		_, _ = io.WriteString(w, "reverse-ok")
	}))
	defer origin.Close()
	originAddr := strings.TrimPrefix(origin.URL, "http://")

	const key = "test_key_reverse"
	serverPort := allocateTCPPort(t)
	reversePort := allocateTCPPort(t)

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
					AEADMethod:         "chacha20-poly1305",
					PaddingMin:         ptr(0),
					PaddingMax:         ptr(0),
					ASCII:              "prefer_entropy",
					EnablePureDownlink: ptr(true),
					DisableHTTPMask:    true,
					Reverse: option.SudokuReverseOptions{
						Listen: netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), reversePort).String(),
					},
				},
			},
		},
		Outbounds: []option.Outbound{{Type: C.TypeDirect, Tag: "direct"}},
		Route:     &option.RouteOptions{Final: "direct"},
	}
	startBox(t, serverOpts)
	waitTCPPort(t, serverPort, "sudoku server")
	waitTCPPort(t, reversePort, "sudoku reverse entry")

	clientOpts := option.Options{
		Log: &option.LogOptions{Level: "warning"},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeSudoku,
				Tag:  "sudoku-reverse-out",
				Options: &option.SudokuOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Key:                key,
					AEADMethod:         "chacha20-poly1305",
					PaddingMin:         ptr(0),
					PaddingMax:         ptr(0),
					ASCII:              "prefer_entropy",
					EnablePureDownlink: ptr(true),
					DisableHTTPMask:    true,
					Reverse: option.SudokuReverseOptions{
						ClientID: "sing-box-test",
						Routes: []option.SudokuReverseRoute{
							{Path: "/app", Target: originAddr},
						},
					},
				},
			},
		},
		Route: &option.RouteOptions{Final: "sudoku-reverse-out"},
	}
	startBox(t, clientOpts)

	httpClient := &http.Client{Timeout: 5 * time.Second}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := httpClient.Get("http://" + netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), reversePort).String() + "/app/hello")
		require.NoError(c, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		require.Equal(c, http.StatusOK, resp.StatusCode, "body=%q", string(body))
		require.Equal(c, "reverse-ok", string(body))
	}, 5*time.Second, 100*time.Millisecond)
}
