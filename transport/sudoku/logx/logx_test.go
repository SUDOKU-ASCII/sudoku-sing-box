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
package logx

import (
	"os"
	"regexp"
	"testing"
)

var clockRe = regexp.MustCompile(`\d\d:\d\d:\d\d`)

// captureStdout runs fn with os.Stdout redirected to a file and returns what it wrote, with the clock masked.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = orig }()
	fn()
	f.Close()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return clockRe.ReplaceAllString(string(b), "CLOCK")
}

func withStyle(t *testing.T, color bool, level Level) {
	t.Helper()
	initOnce.Do(initConfig)
	savedColor, savedLevel := useColor, minLevel
	useColor, minLevel = color, level
	t.Cleanup(func() { useColor, minLevel = savedColor, savedLevel })
}

func TestLineFormat(t *testing.T) {
	tests := []struct {
		name  string
		color bool
		log   func()
		want  string
	}{
		{"info", false, func() { Infof("Server", "hello %d", 1) }, "CLOCK info [Server] hello 1\n"},
		{"warn", false, func() { Warnf("Server/UoT", "x") }, "CLOCK warn [Server/UoT] x\n"},
		{"error", false, func() { Errorf("A", "x") }, "CLOCK error [A] x\n"},
		{"component is trimmed", false, func() { Infof("  A  ", "x") }, "CLOCK info [A] x\n"},
		{"empty component", false, func() { Infof("", "x") }, "CLOCK info x\n"},
		{"empty message", false, func() { Infof("A", "") }, "CLOCK info [A] \n"},
		{"color", true, func() { Warnf("A", "x") }, "\x1b[2mCLOCK\x1b[0m \x1b[33mwarn\x1b[0m \x1b[35m[A]\x1b[0m x\n"},
		{"color without component", true, func() { Infof("", "x") }, "\x1b[2mCLOCK\x1b[0m \x1b[36minfo\x1b[0m x\n"},
		{"prompt has no newline", false, func() { Promptf("Setup", "name: ") }, "CLOCK info [Setup] name: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withStyle(t, tt.color, LevelInfo)
			if got := captureStdout(t, tt.log); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMinLevelFilters(t *testing.T) {
	withStyle(t, false, LevelWarn)
	got := captureStdout(t, func() {
		Infof("A", "dropped")
		Warnf("A", "kept")
	})
	if want := "CLOCK warn [A] kept\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]Level{
		"": LevelInfo, "info": LevelInfo, " DEBUG ": LevelDebug, "dbg": LevelDebug,
		"warning": LevelWarn, "WARN": LevelWarn, "err": LevelError, "error": LevelError, "bogus": LevelInfo,
	}
	for in, want := range tests {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseLeadingComponents(t *testing.T) {
	tests := []struct{ in, component, rest string }{
		{"plain message", "", "plain message"},
		{"[A] [B] msg", "A/B", "msg"},
		{"  [ A ]  msg", "A", "msg"},
		{"[] msg", "", "[] msg"},
		{"[A msg", "", "[A msg"},
	}
	for _, tt := range tests {
		if c, r := parseLeadingComponents(tt.in); c != tt.component || r != tt.rest {
			t.Errorf("parseLeadingComponents(%q) = %q, %q; want %q, %q", tt.in, c, r, tt.component, tt.rest)
		}
	}
}
