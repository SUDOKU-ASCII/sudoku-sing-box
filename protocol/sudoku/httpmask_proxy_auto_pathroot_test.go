package sudoku_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestSudoku_HTTPMaskAutoPathRoot(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "httpmask-auto-pathroot-ok")
	}))
	defer origin.Close()

	const key = "test_key_interop"

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
					AEADMethod:         "chacha20-poly1305",
					PaddingMin:         ptr(0),
					PaddingMax:         ptr(0),
					ASCII:              "prefer_ascii",
					EnablePureDownlink: ptr(true),
					DisableHTTPMask:    false,
					HTTPMaskMode:       "auto",
					HTTPMaskMultiplex:  "off",
					HTTPMaskPathRoot:   "aabbcc",
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
	waitTCPPort(t, serverPort, "sudoku server")

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
				Type: C.TypeSudoku,
				Tag:  "sudoku-out",
				Options: &option.SudokuOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Key:                key,
					AEADMethod:         "chacha20-poly1305",
					PaddingMin:         ptr(0),
					PaddingMax:         ptr(0),
					ASCII:              "prefer_ascii",
					EnablePureDownlink: ptr(true),
					DisableHTTPMask:    false,
					HTTPMaskMode:       "auto",
					HTTPMaskMultiplex:  "off",
					HTTPMaskPathRoot:   "aabbcc",
				},
			},
		},
		Route: &option.RouteOptions{Final: "sudoku-out"},
	}
	startBox(t, clientOpts)
	waitTCPPort(t, clientPort, "mixed client")

	proxyURL := mustParseURL(t, fmt.Sprintf("http://127.0.0.1:%d", clientPort))
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			DisableKeepAlives: true,
		},
		Timeout: 10 * time.Second,
	}

	resp, err := httpClient.Get(origin.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body=%q", string(body))
	require.Equal(t, "httpmask-auto-pathroot-ok", string(body))
}

func mustParseURL(t testing.TB, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}
