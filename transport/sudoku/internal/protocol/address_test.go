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
package protocol

import (
	"bytes"
	"testing"
)

func TestWriteReadAddress_IPv4(t *testing.T) {
	buf := new(bytes.Buffer)
	addr := "1.2.3.4:8080"

	if err := WriteAddress(buf, addr); err != nil {
		t.Fatalf("WriteAddress error: %v", err)
	}

	got, typ, ip, err := ReadAddress(buf)
	if err != nil {
		t.Fatalf("ReadAddress error: %v", err)
	}
	if got != addr {
		t.Fatalf("addr mismatch, got %s", got)
	}
	if typ != AddrTypeIPv4 {
		t.Fatalf("type mismatch, got %d", typ)
	}
	if ip == nil {
		t.Fatalf("ip should not be nil for ipv4")
	}
}

func TestWriteReadAddress_Domain(t *testing.T) {
	buf := new(bytes.Buffer)
	addr := "example.com:53"

	if err := WriteAddress(buf, addr); err != nil {
		t.Fatalf("WriteAddress error: %v", err)
	}

	got, typ, ip, err := ReadAddress(buf)
	if err != nil {
		t.Fatalf("ReadAddress error: %v", err)
	}
	if got != addr {
		t.Fatalf("addr mismatch, got %s", got)
	}
	if typ != AddrTypeDomain {
		t.Fatalf("type mismatch, got %d", typ)
	}
	if ip != nil {
		t.Fatalf("expected nil ip for domain, got %v", ip)
	}
}

func TestWriteReadAddress_IPv6(t *testing.T) {
	buf := new(bytes.Buffer)
	addr := "[2001:db8::1]:443"

	if err := WriteAddress(buf, addr); err != nil {
		t.Fatalf("WriteAddress error: %v", err)
	}

	got, typ, ip, err := ReadAddress(buf)
	if err != nil {
		t.Fatalf("ReadAddress error: %v", err)
	}
	if got != addr {
		t.Fatalf("addr mismatch, got %s", got)
	}
	if typ != AddrTypeIPv6 {
		t.Fatalf("type mismatch, got %d", typ)
	}
	if ip == nil {
		t.Fatalf("ip should not be nil for ipv6")
	}
}

func TestWriteAddress_DomainTooLong(t *testing.T) {
	longDomain := make([]byte, 256)
	for i := range longDomain {
		longDomain[i] = 'a'
	}
	err := WriteAddress(new(bytes.Buffer), string(longDomain)+":80")
	if err == nil {
		t.Fatalf("expected error for long domain")
	}
}

func TestReadAddress_UnknownType(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0x02, 0x00, 0x50}) // invalid type, port after
	if _, _, _, err := ReadAddress(buf); err == nil {
		t.Fatalf("expected error for unknown type")
	}
}
