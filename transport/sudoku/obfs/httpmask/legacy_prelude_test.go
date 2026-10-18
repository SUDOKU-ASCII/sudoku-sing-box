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

func TestConsumeHeader_ReturnsConsumedData(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "Valid POST request",
			input: "POST /api/v1/upload HTTP/1.1\r\n" +
				"Host: example.com\r\n" +
				"User-Agent: Go-Test\r\n" +
				"\r\n" +
				"Body data",
			wantErr: false,
		},
		{
			name: "Valid GET request",
			input: "GET /ws HTTP/1.1\r\n" +
				"Host: example.com\r\n" +
				"\r\n",
			wantErr: false,
		},
		{
			name: "Invalid method",
			input: "BREW / HTTP/1.1\r\n" +
				"Host: example.com\r\n" +
				"\r\n",
			wantErr: true,
		},
		{
			name:    "Garbage data",
			input:   "NotHTTPData\r\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			consumed, err := ConsumeHeader(r)

			if (err != nil) != tt.wantErr {
				t.Errorf("ConsumeHeader() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Consumption must stop exactly after the blank line, leaving the body unread.
			if !strings.HasPrefix(tt.input, string(consumed)) {
				t.Errorf("ConsumeHeader() consumed data mismatch.\nGot: %q\nInput starts with: %q", consumed, tt.input[:len(consumed)])
			}
			if tt.wantErr {
				return
			}
			header, body, _ := strings.Cut(tt.input, "\r\n\r\n")
			if want := header + "\r\n\r\n"; string(consumed) != want {
				t.Errorf("ConsumeHeader() consumed %q, want %q", consumed, want)
			}
			rest, _ := r.ReadString('\n')
			if rest != body {
				t.Errorf("ConsumeHeader() left %q in the reader, want %q", rest, body)
			}
		})
	}
}

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
