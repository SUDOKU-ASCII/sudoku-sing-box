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
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
)

func TestUoTWriter_RoundTrip(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() })
	w := NewUoTWriter(pw, nil)
	t.Cleanup(w.Close)

	cases := []struct {
		addr    string
		payload []byte
	}{
		{"example.com:53", []byte("hello uot")},
		{"1.2.3.4:5000", nil},
		{"[2001:db8::1]:443", bytes.Repeat([]byte{0xab}, 1200)},
	}
	for _, c := range cases {
		if err := w.WriteDatagram(c.addr, c.payload); err != nil {
			t.Fatalf("WriteDatagram(%s): %v", c.addr, err)
		}
	}
	for _, c := range cases {
		addr, payload, err := ReadUoTDatagram(pr)
		if err != nil {
			t.Fatalf("ReadUoTDatagram: %v", err)
		}
		if addr != c.addr || !bytes.Equal(payload, c.payload) {
			t.Fatalf("got (%q, %d bytes), want (%q, %d bytes)", addr, len(payload), c.addr, len(c.payload))
		}
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

func TestUoTReader_ReusesItsBuffer(t *testing.T) {
	src := &cycleReader{data: uotBenchFrame(netip.MustParseAddrPort("203.0.113.7:443"), 1200)}
	r := NewUoTReader(src)
	if _, _, err := r.ReadDatagram(); err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		host, payload, err := r.ReadDatagram()
		if err != nil || host != "203.0.113.7:443" || len(payload) != 1200 {
			t.Fatalf("ReadDatagram = (%q, %d bytes, %v)", host, len(payload), err)
		}
	})
	if allocs > 1 {
		t.Fatalf("ReadDatagram allocated %v times per datagram, want only the address string", allocs)
	}
}

func TestUoTDestinations_CacheDomainsBriefly(t *testing.T) {
	want := netip.MustParseAddrPort("198.51.100.9:5353")
	calls := 0
	failing := false
	d := newUoTDestinations()
	d.resolve = func(hostPort string) (netip.AddrPort, error) {
		calls++
		if hostPort != "dns.example:5353" {
			t.Fatalf("resolved %q", hostPort)
		}
		if failing {
			return netip.AddrPort{}, errors.New("no such host")
		}
		return want, nil
	}
	domain, _ := protocol.AppendAddress(nil, "dns.example:5353")
	ipv4, _ := protocol.AppendAddress(nil, "192.0.2.1:53")
	now := time.Unix(1_000_000, 0)

	if ap, err := d.lookup(ipv4, now); err != nil || ap != netip.MustParseAddrPort("192.0.2.1:53") || calls != 0 {
		t.Fatalf("IP destination = (%v, %v) after %d resolutions", ap, err, calls)
	}
	for i := range 3 {
		ap, err := d.lookup(domain, now.Add(time.Duration(i)*time.Second))
		if err != nil || ap != want {
			t.Fatalf("lookup %d = (%v, %v)", i, ap, err)
		}
	}
	if calls != 1 {
		t.Fatalf("resolved %d times within the TTL, want 1", calls)
	}

	if _, err := d.lookup(domain, now.Add(uotDestinationTTL)); err != nil || calls != 2 {
		t.Fatalf("expired entry was not resolved again (err=%v, calls=%d)", err, calls)
	}

	failing = true
	later := now.Add(10 * uotDestinationTTL)
	for range 2 {
		if _, err := d.lookup(domain, later); err == nil {
			t.Fatal("expected a resolution error")
		}
	}
	if calls != 4 {
		t.Fatalf("a failed lookup was cached (calls=%d)", calls)
	}
}

func TestUoTDestinations_CacheIsBounded(t *testing.T) {
	d := newUoTDestinations()
	d.resolve = func(string) (netip.AddrPort, error) { return netip.MustParseAddrPort("198.51.100.9:1"), nil }
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 3*uotDestinationCacheLimit; i++ {
		addr, err := protocol.AppendAddress(nil, fmt.Sprintf("host-%d.example:80", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.lookup(addr, now); err != nil {
			t.Fatal(err)
		}
		if len(d.cache) > uotDestinationCacheLimit {
			t.Fatalf("cache grew to %d entries", len(d.cache))
		}
	}
}

func TestHandleUoTServer_DomainDestinationRoundTrip(t *testing.T) {
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skipf("udp loopback unavailable: %v", err)
	}
	client, server := net.Pipe()
	w := NewUoTWriter(client, nil)
	t.Cleanup(func() {
		w.Close()
		_ = client.Close()
		_ = server.Close()
		_ = peer.Close()
	})
	go func() { _ = HandleUoTServer(server) }()

	port := peer.LocalAddr().(*net.UDPAddr).Port
	_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
	var from *net.UDPAddr
	for _, msg := range []string{"one", "two"} {
		if err := w.WriteDatagram(fmt.Sprintf("localhost:%d", port), []byte(msg)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		n, addr, err := peer.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("peer read %q: %v", msg, err)
		}
		if string(buf[:n]) != msg {
			t.Fatalf("peer got %q, want %q", buf[:n], msg)
		}
		from = addr
	}

	if _, err := peer.WriteToUDP([]byte("pong"), from); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	host, payload, err := NewUoTReader(client).ReadDatagram()
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if want := fmt.Sprintf("127.0.0.1:%d", port); host != want || string(payload) != "pong" {
		t.Fatalf("reply = (%q, %q), want (%q, \"pong\")", host, payload, want)
	}
}
