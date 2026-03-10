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
package crypto

import (
	"io"
	"net"
	"testing"
)

func TestAEADConnRoundTrip_Chacha(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	connA, err := NewAEADConn(left, "secret-key", "chacha20-poly1305")
	if err != nil {
		t.Fatalf("NewAEADConn A error: %v", err)
	}
	connB, err := NewAEADConn(right, "secret-key", "chacha20-poly1305")
	if err != nil {
		t.Fatalf("NewAEADConn B error: %v", err)
	}

	msg := []byte("hello aead")
	go func() {
		defer connA.Close()
		if _, err := connA.Write(msg); err != nil {
			t.Errorf("write failed: %v", err)
		}
	}()

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(connB, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("payload mismatch, got %q", string(buf))
	}
}

func TestAEADConnNone_Passthrough(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	connA, err := NewAEADConn(left, "ignored", "none")
	if err != nil {
		t.Fatalf("NewAEADConn A error: %v", err)
	}
	connB, err := NewAEADConn(right, "ignored", "none")
	if err != nil {
		t.Fatalf("NewAEADConn B error: %v", err)
	}

	msg := []byte("plain text")
	go connA.Write(msg)

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(connB, buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("payload mismatch, got %q", string(buf))
	}
}

func TestAEADConnUnsupported(t *testing.T) {
	if _, err := NewAEADConn(nil, "key", "invalid"); err == nil {
		t.Fatalf("expected error for unsupported cipher")
	}
}
