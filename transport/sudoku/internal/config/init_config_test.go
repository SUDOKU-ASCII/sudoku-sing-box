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
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.json")

	data := `{
		"mode": "client",
		"local_port": 8080,
		"server_address": "1.1.1.1:443",
		"key": "k",
		"aead": "none",
		"rule_urls": ["global"]
	}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Transport != "tcp" {
		t.Fatalf("Transport default not applied")
	}
	if cfg.ASCII != "prefer_entropy" {
		t.Fatalf("ASCII default not applied, got %s", cfg.ASCII)
	}
	if cfg.ProxyMode != "global" || cfg.RuleURLs != nil {
		t.Fatalf("ProxyMode parsing failed, mode=%s urls=%v", cfg.ProxyMode, cfg.RuleURLs)
	}
	if !cfg.EnablePureDownlink {
		t.Fatalf("EnablePureDownlink should default to true")
	}
}

func TestLoadAllowsPackedWithoutAEAD(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.json")

	data := `{
		"mode": "server",
		"local_port": 8080,
		"server_address": "0.0.0.0:8080",
		"key": "k",
		"aead": "none",
		"enable_pure_downlink": false
	}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("packed downlink with AEAD none should load: %v", err)
	}
	if cfg.EnablePureDownlink {
		t.Fatalf("expected packed downlink to remain enabled")
	}
	if cfg.AEAD != "none" {
		t.Fatalf("unexpected AEAD after load: %s", cfg.AEAD)
	}
}

func TestLoadHTTPMaskPathRoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		extraJSON   string
		expectValue string
	}{
		{name: "httpmask-object", extraJSON: `"httpmask": {"path_root": "aabbcc"}`, expectValue: "aabbcc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "cfg.json")

			data := fmt.Sprintf(`{
				"mode": "client",
				"local_port": 8080,
				"server_address": "1.1.1.1:443",
				"key": "k",
				"aead": "none",
				%s,
				"rule_urls": ["global"]
			}`, tc.extraJSON)

			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load error: %v", err)
			}
			if cfg.HTTPMask.PathRoot != tc.expectValue {
				t.Fatalf("HTTPMask.PathRoot mismatch: got %q want %q", cfg.HTTPMask.PathRoot, tc.expectValue)
			}
		})
	}
}
