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
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
)

func canonicalHeaderHost(urlHost, scheme string) string {
	host, port, err := net.SplitHostPort(urlHost)
	if err != nil {
		return urlHost
	}

	defaultPort := ""
	switch scheme {
	case "https":
		defaultPort = "443"
	case "http":
		defaultPort = "80"
	}
	if defaultPort == "" || port != defaultPort {
		return urlHost
	}

	// If we strip the port from an IPv6 literal, re-add brackets to keep the Host header valid.
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func normalizeHTTPDialTarget(serverAddress string, tlsEnabled bool, hostOverride string) (scheme, urlHost, dialAddr, serverName string, err error) {
	host, port, err := net.SplitHostPort(serverAddress)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid server address %q: %w", serverAddress, err)
	}
	scheme = "http"
	if tlsEnabled {
		scheme = "https"
	}
	urlHost, dialAddr, serverName = resolveDialTarget(host, port, hostOverride)
	return scheme, urlHost, dialAddr, serverName, nil
}

// resolveDialTarget applies HostOverride ("example.com" or "example.com:443"):
// it changes the Host header and SNI and may change the port, but the TCP
// connection still goes to host.
func resolveDialTarget(host, port, hostOverride string) (urlHost, dialAddr, serverName string) {
	serverName = host
	if hostOverride != "" {
		if h, p, err := net.SplitHostPort(hostOverride); err == nil {
			if h != "" {
				hostOverride = h
			}
			if p != "" {
				port = p
			}
		}
		serverName = hostOverride
	}
	return net.JoinHostPort(serverName, port), net.JoinHostPort(host, port), trimPortForHost(serverName)
}

// Header values are prebuilt and shared by every request: net/http only reads them, and a fresh []string per
// header would be most of what applying the profile costs.
var (
	userAgentValues      = singleValues(userAgents[:])
	acceptValues         = singleValues(accepts[:])
	acceptLanguageValues = singleValues(acceptLanguages[:])
	noCacheValue         = []string{"no-cache"}
	keepAliveValue       = []string{"keep-alive"}
	tunnelModeValues     = map[TunnelMode][]string{
		TunnelModeLegacy: {string(TunnelModeLegacy)},
		TunnelModeStream: {string(TunnelModeStream)},
		TunnelModePoll:   {string(TunnelModePoll)},
		TunnelModeAuto:   {string(TunnelModeAuto)},
		TunnelModeWS:     {string(TunnelModeWS)},
	}
)

func singleValues(vs []string) [][]string {
	out := make([][]string, len(vs))
	for i, v := range vs {
		out[i] = []string{v}
	}
	return out
}

func applyTunnelHeaders(h http.Header, host string, mode TunnelMode) {
	h["User-Agent"] = userAgentValues[rand.IntN(len(userAgentValues))]
	h["Accept"] = acceptValues[rand.IntN(len(acceptValues))]
	h["Accept-Language"] = acceptLanguageValues[rand.IntN(len(acceptLanguageValues))]
	h["Cache-Control"] = noCacheValue
	h["Pragma"] = noCacheValue
	if mode != TunnelModeWS {
		// The WebSocket handshake owns the Connection header.
		h["Connection"] = keepAliveValue
	}
	h["Host"] = []string{host}
	modeValue, ok := tunnelModeValues[mode]
	if !ok {
		modeValue = []string{string(mode)}
	}
	h["X-Sudoku-Tunnel"] = modeValue
}
