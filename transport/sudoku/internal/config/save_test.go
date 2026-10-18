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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveWritesIndentedJSONAndCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "client.json")
	want := &Config{Mode: "client", ServerAddress: "example.com:443", LocalPort: 1080}

	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "{\n  \"") {
		t.Fatalf("config is not indented with two spaces:\n%s", data)
	}
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || got.ServerAddress != want.ServerAddress || got.LocalPort != want.LocalPort {
		t.Fatalf("round trip = %+v, want %+v", got, *want)
	}
}

func TestSaveReportsUnwritablePath(t *testing.T) {
	if err := Save(t.TempDir(), &Config{}); err == nil {
		t.Fatal("saving over a directory succeeded")
	}
}
