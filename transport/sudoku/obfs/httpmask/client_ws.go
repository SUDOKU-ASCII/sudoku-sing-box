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
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/sagernet/sing-box/transport/sudoku/dnsutil"
)

func normalizeWSSchemeFromAddress(serverAddress string, tlsEnabled bool) (string, string) {
	addr := strings.TrimSpace(serverAddress)
	if strings.Contains(addr, "://") {
		if u, err := url.Parse(addr); err == nil && u != nil {
			switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
			case "ws":
				return "ws", u.Host
			case "wss":
				return "wss", u.Host
			}
		}
	}
	if tlsEnabled {
		return "wss", addr
	}
	return "ws", addr
}

func normalizeWSDialTarget(serverAddress string, tlsEnabled bool, hostOverride string) (scheme, urlHost, dialAddr, serverName string, err error) {
	scheme, addr := normalizeWSSchemeFromAddress(serverAddress, tlsEnabled)

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Allow ws(s)://host without port.
		if strings.Contains(addr, ":") {
			return "", "", "", "", fmt.Errorf("invalid server address %q: %w", serverAddress, err)
		}
		host, port = addr, "80"
		if scheme == "wss" {
			port = "443"
		}
	}
	urlHost, dialAddr, serverName = resolveDialTarget(host, port, hostOverride)
	return scheme, urlHost, dialAddr, serverName, nil
}

func dialWS(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	scheme, urlHost, dialAddr, serverName, err := normalizeWSDialTarget(serverAddress, opts.TLSEnabled, opts.HostOverride)
	if err != nil {
		return nil, err
	}

	httpScheme := "http"
	if scheme == "wss" {
		httpScheme = "https"
	}
	headerHost := canonicalHeaderHost(urlHost, httpScheme)
	auth := newTunnelAuth(opts.AuthKey, 0)

	u := (&url.URL{
		Scheme: scheme,
		Host:   urlHost,
		Path:   joinPathRoot(opts.PathRoot, "/ws"),
	}).String()
	if opts.EarlyHandshake != nil && len(opts.EarlyHandshake.RequestPayload) > 0 {
		u, err = setEarlyDataQuery(u, opts.EarlyHandshake.RequestPayload)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Host = headerHost
	applyTunnelHeaders(req.Header, headerHost, TunnelModeWS)
	applyTunnelAuth(req, auth, TunnelModeWS, http.MethodGet, "/ws")

	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		ResponseHeaderTimeout: 20 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			d := dnsutil.OutboundDialer(0)
			if addr == urlHost {
				addr = dialAddr
			}
			return d.DialContext(dialCtx, network, addr)
		},
	}
	if scheme == "wss" {
		tr.TLSClientConfig = &tls.Config{
			ServerName: serverName,
			MinVersion: tls.VersionTLS12,
		}
	}
	client := &http.Client{Transport: tr}

	c, resp, err := websocket.Dial(ctx, req.URL.String(), &websocket.DialOptions{
		HTTPClient:      client,
		HTTPHeader:      req.Header,
		Host:            headerHost,
		CompressionMode: websocket.CompressionDisabled,
	})
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	if opts.EarlyHandshake != nil && opts.EarlyHandshake.HandleResponse != nil && resp != nil {
		if earlyVal := strings.TrimSpace(resp.Header.Get(tunnelEarlyDataHeader)); earlyVal != "" {
			decoded, decErr := base64.RawURLEncoding.DecodeString(earlyVal)
			if decErr != nil {
				_ = c.Close(websocket.StatusInternalError, "bad early data")
				return nil, decErr
			}
			if err := opts.EarlyHandshake.HandleResponse(decoded); err != nil {
				_ = c.Close(websocket.StatusInternalError, "early handshake failed")
				return nil, err
			}
		}
	}
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
}
