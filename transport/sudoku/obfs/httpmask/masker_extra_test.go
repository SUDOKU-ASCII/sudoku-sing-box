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
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestWriteRandomRequestHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRandomRequestHeaderWithPathRoot(&buf, "example.com", ""); err != nil {
		t.Fatalf("WriteRandomRequestHeaderWithPathRoot error: %v", err)
	}
	raw := buf.String()
	if !(strings.HasPrefix(raw, "POST ") || strings.HasPrefix(raw, "GET ")) {
		t.Fatalf("invalid request line: %q", raw)
	}
	if !strings.Contains(raw, "Host: example.com") {
		t.Fatalf("missing host header")
	}
	if !strings.Contains(raw, "\r\n\r\n") {
		t.Fatalf("missing header terminator")
	}
}

func TestConsumeHeader(t *testing.T) {
	req := "POST /test HTTP/1.1\r\nHost: a\r\n\r\nBODY"
	r := bufio.NewReader(strings.NewReader(req))
	consumed, err := ConsumeHeader(r)
	if err != nil {
		t.Fatalf("ConsumeHeader error: %v", err)
	}
	if string(consumed) != "POST /test HTTP/1.1\r\nHost: a\r\n\r\n" {
		t.Fatalf("unexpected consumed data: %q", string(consumed))
	}
	rest, _ := r.ReadString('\n')
	if rest != "BODY" {
		t.Fatalf("body not left in reader, got %q", rest)
	}
}
