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
	"reflect"
	"testing"
)

func TestFinalizeReverseRoutes(t *testing.T) {
	tests := []struct {
		name      string
		reverse   *ReverseConfig
		wantPaths []string
		wantNil   bool
		wantErr   string
	}{
		{name: "path is canonicalized", reverse: &ReverseConfig{Routes: []ReverseRoute{{Path: " gitea// ", Target: "a:1"}}}, wantPaths: []string{"/gitea"}},
		{name: "blank route is dropped", reverse: &ReverseConfig{Routes: []ReverseRoute{{}, {Path: "/a", Target: "h:1"}}}, wantPaths: []string{"/a"}},
		{name: "empty path is the raw tcp route", reverse: &ReverseConfig{Routes: []ReverseRoute{{Target: "h:1"}}}, wantPaths: []string{""}},
		{name: "reverse with nothing configured is removed", reverse: &ReverseConfig{Listen: " ", Routes: []ReverseRoute{{}}}, wantNil: true},
		{name: "missing target", reverse: &ReverseConfig{Routes: []ReverseRoute{{Path: "/a"}}}, wantErr: `reverse route "/a": missing target`},
		{name: "tcp route with bad target", reverse: &ReverseConfig{Routes: []ReverseRoute{{Target: "bad"}}}, wantErr: `reverse tcp route: invalid target "bad": address bad: missing port in address`},
		{name: "duplicate path after canonicalization", reverse: &ReverseConfig{Routes: []ReverseRoute{{Path: "/a", Target: "h:1"}, {Path: "a/", Target: "h:2"}}}, wantErr: `reverse route duplicate path: "/a"`},
		{name: "duplicate tcp route", reverse: &ReverseConfig{Routes: []ReverseRoute{{Target: "h:1"}, {Target: "h:2"}}}, wantErr: "reverse route duplicate tcp mapping"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Reverse: tc.reverse}
			err := cfg.Finalize()
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Finalize() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Finalize() error = %v", err)
			}
			if tc.wantNil {
				if cfg.Reverse != nil {
					t.Fatalf("Reverse = %+v, want nil", cfg.Reverse)
				}
				return
			}
			var got []string
			for _, r := range cfg.Reverse.Routes {
				got = append(got, r.Path)
			}
			if !reflect.DeepEqual(got, tc.wantPaths) {
				t.Fatalf("route paths = %q, want %q", got, tc.wantPaths)
			}
		})
	}
}

func TestFinalizeProxyMode(t *testing.T) {
	tests := []struct {
		name      string
		ruleURLs  []string
		proxyMode string
		wantMode  string
		wantRules []string
	}{
		{name: "global keyword overrides pac and clears rules", ruleURLs: []string{"global"}, proxyMode: "pac", wantMode: "global"},
		{name: "direct keyword is case and space insensitive", ruleURLs: []string{" Direct "}, wantMode: "direct"},
		{name: "any other rule source means pac", ruleURLs: []string{"https://example.test/r.yaml"}, proxyMode: "global", wantMode: "pac", wantRules: []string{"https://example.test/r.yaml"}},
		{name: "keyword among several sources is just a source", ruleURLs: []string{"global", "https://example.test/r.yaml"}, wantMode: "pac", wantRules: []string{"global", "https://example.test/r.yaml"}},
		{name: "unknown mode falls back to global", proxyMode: "bogus", wantMode: "global"},
		{name: "explicit direct without rules is kept", proxyMode: "DIRECT", wantMode: "direct"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{RuleURLs: tc.ruleURLs, ProxyMode: tc.proxyMode}
			if err := cfg.Finalize(); err != nil {
				t.Fatalf("Finalize() error = %v", err)
			}
			if cfg.ProxyMode != tc.wantMode {
				t.Fatalf("ProxyMode = %q, want %q", cfg.ProxyMode, tc.wantMode)
			}
			if !reflect.DeepEqual(cfg.RuleURLs, tc.wantRules) {
				t.Fatalf("RuleURLs = %q, want %q", cfg.RuleURLs, tc.wantRules)
			}
		})
	}
}
