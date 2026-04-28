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
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

// shortLinkPayload holds the minimal fields we expose in sudoku:// links.
type shortLinkPayload struct {
	Host           string   `json:"h"`            // server host / IP
	Port           int      `json:"p"`            // server port
	Key            string   `json:"k"`            // shared key
	ASCII          string   `json:"a,omitempty"`  // "ascii", "entropy", or directional "up_*_down_*"
	AEAD           string   `json:"e,omitempty"`  // AEAD method
	MixPort        int      `json:"m,omitempty"`  // local mixed proxy port
	PackedDownlink bool     `json:"x,omitempty"`  // bandwidth-optimized downlink (non-pure Sudoku)
	CustomTable    string   `json:"t,omitempty"`  // optional custom byte layout
	CustomTables   []string `json:"ts,omitempty"` // optional custom byte layouts (rotation)
	// HTTP mask / tunnel controls (optional).
	DisableHTTPMask bool   `json:"hd,omitempty"` // when true, disable HTTP mask completely
	HTTPMaskMode    string `json:"hm,omitempty"` // "legacy" / "stream" / "poll" / "auto" / "ws"
	HTTPMaskTLS     bool   `json:"ht,omitempty"` // enable HTTPS (when false/omitted, use plain HTTP)
	HTTPMaskHost    string `json:"hh,omitempty"` // override HTTP Host/SNI in tunnel modes
	HTTPMaskMux     string `json:"hx,omitempty"` // "off" / "auto" / "on"
	HTTPMaskPath    string `json:"hy,omitempty"` // optional first-level path root prefix
}

// BuildShortLinkFromConfig builds a sudoku:// short link from the provided config.
//
// If cfg.ServerAddress is empty, advertiseHost can be used to provide the public host[:port].
// For server configs, when advertiseHost is empty, we try to derive the host from fallback_address (host part)
// and use local_port as the advertised port.
func BuildShortLinkFromConfig(cfg *Config, advertiseHost string) (string, error) {
	if cfg == nil {
		return "", errors.New("nil config")
	}

	host, port, err := deriveAdvertiseAddress(cfg, advertiseHost)
	if err != nil {
		return "", err
	}

	payload := shortLinkPayload{
		Host: host,
		Port: port,
		Key:  cfg.Key,
		AEAD: cfg.AEAD,
	}

	if cfg.Mode == "client" && cfg.LocalPort > 0 {
		payload.MixPort = cfg.LocalPort
	}
	if payload.MixPort == 0 {
		payload.MixPort = 1080 // reasonable default for mixed proxy
	}

	payload.PackedDownlink = !cfg.EnablePureDownlink
	payload.CustomTable = cfg.CustomTable
	if len(cfg.CustomTables) > 0 {
		payload.CustomTables = append([]string(nil), cfg.CustomTables...)
	}

	payload.DisableHTTPMask = cfg.HTTPMask.Disable
	mode := strings.ToLower(strings.TrimSpace(cfg.HTTPMask.Mode))
	if mode != "" && mode != "legacy" {
		payload.HTTPMaskMode = mode
	}
	if cfg.HTTPMask.TLS {
		payload.HTTPMaskTLS = true
	}
	if strings.TrimSpace(cfg.HTTPMask.Host) != "" {
		payload.HTTPMaskHost = strings.TrimSpace(cfg.HTTPMask.Host)
	}
	if strings.TrimSpace(cfg.HTTPMask.PathRoot) != "" {
		payload.HTTPMaskPath = strings.TrimSpace(cfg.HTTPMask.PathRoot)
	}
	muxMode := strings.ToLower(strings.TrimSpace(cfg.HTTPMask.Multiplex))
	if muxMode != "" && muxMode != "off" {
		payload.HTTPMaskMux = muxMode
	}

	payload.ASCII = encodeASCII(cfg.ASCII)
	if payload.AEAD == "" {
		payload.AEAD = "chacha20-poly1305"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return "sudoku://" + base64.RawURLEncoding.EncodeToString(data), nil
}

// BuildConfigFromShortLink parses a sudoku:// short link and returns a client config.
// The generated config is ready to run a PAC proxy.
func BuildConfigFromShortLink(link string) (*Config, error) {
	if !strings.HasPrefix(link, "sudoku://") {
		return nil, errors.New("invalid scheme")
	}

	encoded := strings.TrimPrefix(link, "sudoku://")
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode short link failed: %w", err)
	}

	var payload shortLinkPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid short link payload: %w", err)
	}

	if payload.Host == "" || payload.Port == 0 || payload.Key == "" {
		return nil, errors.New("short link missing required fields")
	}

	cfg := &Config{
		Mode:          "client",
		Transport:     "tcp",
		LocalPort:     payload.MixPort,
		ServerAddress: net.JoinHostPort(payload.Host, strconv.Itoa(payload.Port)),
		Key:           payload.Key,
		CustomTable:   payload.CustomTable,
		CustomTables:  append([]string(nil), payload.CustomTables...),
		HTTPMask: HTTPMaskConfig{
			Disable:   payload.DisableHTTPMask,
			Mode:      payload.HTTPMaskMode,
			TLS:       payload.HTTPMaskTLS,
			Host:      payload.HTTPMaskHost,
			PathRoot:  strings.TrimSpace(payload.HTTPMaskPath),
			Multiplex: strings.TrimSpace(payload.HTTPMaskMux),
		},
		AEAD:       payload.AEAD,
		PaddingMin: 5,
		PaddingMax: 15,
		ProxyMode:  "pac",
		RuleURLs:   DefaultPACRuleURLs(),
	}

	if cfg.LocalPort == 0 {
		cfg.LocalPort = 1080
	}

	cfg.EnablePureDownlink = !payload.PackedDownlink
	cfg.ASCII = decodeASCII(payload.ASCII)
	if cfg.AEAD == "" {
		cfg.AEAD = "none"
	}

	if err := cfg.Finalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func encodeASCII(mode string) string {
	normalized, err := sudoku.NormalizeASCIIMode(mode)
	if err != nil {
		normalized = strings.ToLower(strings.TrimSpace(mode))
	}
	switch normalized {
	case "prefer_ascii", "ascii":
		return "ascii"
	case "", "prefer_entropy", "entropy":
		return "entropy"
	default:
		return normalized
	}
}

func decodeASCII(val string) string {
	normalized, err := sudoku.NormalizeASCIIMode(val)
	if err == nil {
		return normalized
	}
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "ascii", "prefer_ascii":
		return "prefer_ascii"
	default:
		return "prefer_entropy"
	}
}

func deriveAdvertiseAddress(cfg *Config, advertiseHost string) (string, int, error) {
	if cfg.ServerAddress != "" {
		host, portStr, err := net.SplitHostPort(cfg.ServerAddress)
		if err != nil {
			return "", 0, fmt.Errorf("invalid server_address: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port in server_address: %w", err)
		}
		return host, port, nil
	}

	if advertiseHost != "" {
		// Allow advertiseHost in either "host" form (use cfg.LocalPort) or "host:port" form (explicit port).
		if h, p, err := net.SplitHostPort(advertiseHost); err == nil && h != "" && p != "" {
			port, err := strconv.Atoi(p)
			if err != nil {
				return "", 0, fmt.Errorf("invalid port in advertise host: %w", err)
			}
			return h, port, nil
		}
		if cfg.LocalPort > 0 {
			return advertiseHost, cfg.LocalPort, nil
		}
	}

	// Best-effort fallback for server-side configs:
	// if the user didn't provide a public host (CLI) nor server_address (config),
	// try to reuse fallback_address's host as the advertised host.
	//
	// This makes `-export-link` usable with typical server configs where the fallback
	// runs on the same machine (e.g. 127.0.0.1:80) or same public IP but different port.
	if cfg.Mode == "server" && advertiseHost == "" && cfg.LocalPort > 0 && cfg.FallbackAddr != "" {
		if h, _, err := net.SplitHostPort(cfg.FallbackAddr); err == nil && h != "" {
			return h, cfg.LocalPort, nil
		}
	}

	return "", 0, errors.New("cannot derive server address; set server_address or provide advertise host")
}
