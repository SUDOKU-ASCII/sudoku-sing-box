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
	"net"
	"testing"
)

func TestShortLinkRoundTrip_Client(t *testing.T) {
	cfg := &Config{
		Mode:               "client",
		LocalPort:          1081,
		ServerAddress:      "8.8.8.8:443",
		Key:                "deadbeef",
		AEAD:               "aes-128-gcm",
		ASCII:              "prefer_ascii",
		CustomTable:        "xpxvvpvv",
		EnablePureDownlink: false,
	}

	link, err := BuildShortLinkFromConfig(cfg, "")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}
	if link == "" {
		t.Fatalf("empty link")
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}

	if decoded.ServerAddress != cfg.ServerAddress {
		t.Fatalf("server address mismatch, got %s", decoded.ServerAddress)
	}
	if decoded.LocalPort != cfg.LocalPort {
		t.Fatalf("local port mismatch, got %d", decoded.LocalPort)
	}
	if decoded.Key != cfg.Key {
		t.Fatalf("key mismatch, got %s", decoded.Key)
	}
	if decoded.AEAD != cfg.AEAD {
		t.Fatalf("aead mismatch, got %s", decoded.AEAD)
	}
	if decoded.CustomTable != cfg.CustomTable {
		t.Fatalf("custom table mismatch, got %s", decoded.CustomTable)
	}
	if decoded.EnablePureDownlink != cfg.EnablePureDownlink {
		t.Fatalf("downlink mode mismatch")
	}
	if decoded.ASCII != "prefer_ascii" {
		t.Fatalf("ascii mismatch, got %s", decoded.ASCII)
	}
}

func TestShortLinkRoundTrip_CustomTablesAndCDN(t *testing.T) {
	cfg := &Config{
		Mode:               "client",
		LocalPort:          1081,
		ServerAddress:      "edge.example.net:443",
		Key:                "deadbeef",
		AEAD:               "aes-128-gcm",
		ASCII:              "prefer_entropy",
		CustomTables:       []string{"xpxvvpvv", "vxpvxvvp"},
		EnablePureDownlink: true,
		HTTPMask: HTTPMaskConfig{
			Disable:   false,
			Mode:      "auto",
			TLS:       true,
			Multiplex: "auto",
		},
	}

	link, err := BuildShortLinkFromConfig(cfg, "")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}

	if decoded.ServerAddress != cfg.ServerAddress {
		t.Fatalf("server address mismatch, got %s", decoded.ServerAddress)
	}
	if len(decoded.CustomTables) != len(cfg.CustomTables) {
		t.Fatalf("custom tables length mismatch, got %d", len(decoded.CustomTables))
	}
	for i := range cfg.CustomTables {
		if decoded.CustomTables[i] != cfg.CustomTables[i] {
			t.Fatalf("custom tables[%d] mismatch, got %s", i, decoded.CustomTables[i])
		}
	}
	if decoded.CustomTable != "" {
		t.Fatalf("custom table mismatch, got %s", decoded.CustomTable)
	}
	if decoded.HTTPMask.Mode != "auto" {
		t.Fatalf("http mask mode mismatch, got %s", decoded.HTTPMask.Mode)
	}
	if !decoded.HTTPMask.TLS {
		t.Fatalf("http mask tls mismatch, got %v", decoded.HTTPMask.TLS)
	}
	if decoded.HTTPMask.Multiplex != "auto" {
		t.Fatalf("http mask multiplex mismatch, got %s", decoded.HTTPMask.Multiplex)
	}
	if decoded.HTTPMask.Disable {
		t.Fatalf("disable http mask mismatch, got %v", decoded.HTTPMask.Disable)
	}
}

func TestShortLinkRoundTrip_DirectionalASCII(t *testing.T) {
	cfg := &Config{
		Mode:               "client",
		LocalPort:          1081,
		ServerAddress:      "edge.example.net:443",
		Key:                "deadbeef",
		AEAD:               "aes-128-gcm",
		ASCII:              "up_ascii_down_entropy",
		CustomTables:       []string{"xpxvvpvv", "vxpvxvvp"},
		EnablePureDownlink: true,
	}

	link, err := BuildShortLinkFromConfig(cfg, "")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}

	if decoded.ASCII != "up_ascii_down_entropy" {
		t.Fatalf("ascii mismatch, got %s", decoded.ASCII)
	}
	if len(decoded.CustomTables) != len(cfg.CustomTables) {
		t.Fatalf("custom tables length mismatch, got %d", len(decoded.CustomTables))
	}
}

func TestShortLinkIPv6ServerAddress(t *testing.T) {
	serverAddr := net.JoinHostPort("2001:db8::1", "443")
	cfg := &Config{
		Mode:          "client",
		LocalPort:     1081,
		ServerAddress: serverAddr,
		Key:           "deadbeef",
	}

	link, err := BuildShortLinkFromConfig(cfg, "")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}
	if decoded.ServerAddress != serverAddr {
		t.Fatalf("server address mismatch, got %s", decoded.ServerAddress)
	}
}

func TestShortLinkAdvertiseServer(t *testing.T) {
	cfg := &Config{
		Mode:               "server",
		LocalPort:          9443,
		Key:                "deadbeef",
		ASCII:              "",
		AEAD:               "",
		EnablePureDownlink: true,
		FallbackAddr:       "127.0.0.1:80",
	}

	link, err := BuildShortLinkFromConfig(cfg, "example.com")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}
	if link == "" {
		t.Fatalf("empty link")
	}
}

func TestShortLinkAdvertiseHostWithPort(t *testing.T) {
	cfg := &Config{
		Mode:               "server",
		LocalPort:          8080,
		Key:                "deadbeef",
		EnablePureDownlink: true,
		HTTPMask: HTTPMaskConfig{
			Disable: false,
			Mode:    "auto",
		},
	}

	link, err := BuildShortLinkFromConfig(cfg, "edge.example.net:443")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}
	if decoded.ServerAddress != "edge.example.net:443" {
		t.Fatalf("server address mismatch, got %s", decoded.ServerAddress)
	}
	if decoded.HTTPMask.Mode != "auto" {
		t.Fatalf("http mask mode mismatch, got %s", decoded.HTTPMask.Mode)
	}
}

func TestShortLinkServerDeriveHostFromFallback(t *testing.T) {
	cfg := &Config{
		Mode:               "server",
		LocalPort:          10059,
		Key:                "deadbeef",
		EnablePureDownlink: true,
		FallbackAddr:       "8.219.204.112:11415",
		HTTPMask: HTTPMaskConfig{
			Disable: false,
			Mode:    "poll",
		},
	}

	link, err := BuildShortLinkFromConfig(cfg, "")
	if err != nil {
		t.Fatalf("BuildShortLinkFromConfig error: %v", err)
	}

	decoded, err := BuildConfigFromShortLink(link)
	if err != nil {
		t.Fatalf("BuildConfigFromShortLink error: %v", err)
	}

	if decoded.ServerAddress != "8.219.204.112:10059" {
		t.Fatalf("server address mismatch, got %s", decoded.ServerAddress)
	}
	if decoded.HTTPMask.Mode != "poll" {
		t.Fatalf("http mask mode mismatch, got %s", decoded.HTTPMask.Mode)
	}
}

func TestShortLinkInvalidScheme(t *testing.T) {
	if _, err := BuildConfigFromShortLink("http://bad"); err == nil {
		t.Fatalf("expected error for bad scheme")
	}
}

func TestShortLinkMissingFields(t *testing.T) {
	payload := map[string]string{}
	raw, _ := json.Marshal(payload)
	link := "sudoku://" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := BuildConfigFromShortLink(link); err == nil {
		t.Fatalf("expected error for missing fields")
	}
}
