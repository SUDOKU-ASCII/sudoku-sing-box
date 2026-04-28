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
package tunnel

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestUoTDatagram_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	addr := "example.com:53"
	payload := []byte("hello uot")

	if err := WriteUoTDatagram(&buf, addr, payload); err != nil {
		t.Fatalf("WriteUoTDatagram error: %v", err)
	}
	gotAddr, gotPayload, err := ReadUoTDatagram(&buf)
	if err != nil {
		t.Fatalf("ReadUoTDatagram error: %v", err)
	}
	if gotAddr != addr {
		t.Fatalf("addr mismatch: got %q want %q", gotAddr, addr)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload mismatch: got %q want %q", gotPayload, payload)
	}
}

func TestReadUoTDatagram_InvalidAddrLen(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, 4)
	// addrLen=0 => invalid
	binary.BigEndian.PutUint16(header[:2], 0)
	binary.BigEndian.PutUint16(header[2:], 1)
	_, _ = buf.Write(header)
	_, _ = buf.Write([]byte{0x00})

	if _, _, err := ReadUoTDatagram(&buf); err == nil {
		t.Fatalf("expected error")
	}
}

func TestReadUoTDatagram_Truncated(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, 4)
	binary.BigEndian.PutUint16(header[:2], 3) // addrLen
	binary.BigEndian.PutUint16(header[2:], 2) // payloadLen
	_, _ = buf.Write(header)
	_, _ = buf.Write([]byte{0x01, 0x02}) // truncated addr

	if _, _, err := ReadUoTDatagram(&buf); err == nil {
		t.Fatalf("expected error")
	}
}

func TestHandleUoTServer_ConnClosed(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	errCh := make(chan error, 1)
	go func() { errCh <- HandleUoTServer(server) }()

	_ = client.Close()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout")
	}
}
