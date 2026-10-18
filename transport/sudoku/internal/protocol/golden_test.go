package protocol

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestGoldenWriteAddress(t *testing.T) {
	for _, c := range goldenEncode {
		var buf bytes.Buffer
		if err := WriteAddress(&buf, c.addr); err != nil {
			t.Fatalf("WriteAddress(%.40q): %v", c.addr, err)
		}
		if got := hex.EncodeToString(buf.Bytes()); got != c.wire {
			t.Errorf("WriteAddress(%.40q) = %.80s, want %.80s", c.addr, got, c.wire)
		}
	}
	for _, addr := range goldenEncodeReject {
		if err := WriteAddress(io.Discard, addr); err == nil {
			t.Errorf("WriteAddress(%.40q) accepted, reference rejects it", addr)
		}
	}
}

func TestGoldenReadAddress(t *testing.T) {
	for _, c := range goldenDecode {
		host, typ, ip, err := ReadAddress(bytes.NewReader(mustHex(t, c.wire)))
		if err != nil {
			t.Fatalf("ReadAddress(%.40s): %v", c.wire, err)
		}
		ipStr := ""
		if ip != nil {
			ipStr = ip.String()
		}
		if host != c.host || typ != c.typ || ipStr != c.ip {
			t.Errorf("ReadAddress(%.40s) = (%.60q, %d, %q), want (%.60q, %d, %q)", c.wire, host, typ, ipStr, c.host, c.typ, c.ip)
		}
	}
	for _, w := range goldenDecodeReject {
		if _, _, _, err := ReadAddress(bytes.NewReader(mustHex(t, w))); err == nil {
			t.Errorf("ReadAddress(%q) accepted, reference rejects it", w)
		}
	}
}

func TestGoldenAppendAddress(t *testing.T) {
	prefix := []byte{0xaa, 0xbb}
	for _, c := range goldenEncode {
		got, err := AppendAddress(append([]byte(nil), prefix...), c.addr)
		if err != nil {
			t.Fatalf("AppendAddress(%.40q): %v", c.addr, err)
		}
		if !bytes.Equal(got[:2], prefix) || hex.EncodeToString(got[2:]) != c.wire {
			t.Errorf("AppendAddress(%.40q) = %.80x, want aabb%.80s", c.addr, got, c.wire)
		}
	}
	for _, addr := range goldenEncodeReject {
		got, err := AppendAddress(append([]byte(nil), prefix...), addr)
		if err == nil {
			t.Errorf("AppendAddress(%.40q) accepted, reference rejects it", addr)
		}
		if !bytes.Equal(got, prefix) {
			t.Errorf("AppendAddress(%.40q) modified dst on error: %x", addr, got)
		}
	}
}

func TestGoldenDecodeAddress(t *testing.T) {
	for _, c := range goldenDecode {
		wire := mustHex(t, c.wire)
		for _, tail := range [][]byte{nil, {0xde, 0xad}} {
			host, err := DecodeAddress(append(append([]byte(nil), wire...), tail...))
			if err != nil {
				t.Fatalf("DecodeAddress(%.40s): %v", c.wire, err)
			}
			if host != c.host {
				t.Errorf("DecodeAddress(%.40s) = %.60q, want %.60q", c.wire, host, c.host)
			}
		}
	}
	for _, w := range goldenDecodeReject {
		if _, err := DecodeAddress(mustHex(t, w)); err == nil {
			t.Errorf("DecodeAddress(%q) accepted, reference rejects it", w)
		}
	}
}

func TestDecodeAddressBracketsColonDomain(t *testing.T) {
	wire := append([]byte{AddrTypeDomain, 5}, "a:b:c"...)
	wire = append(wire, 0x00, 0x50)
	want, _, _, err := ReadAddress(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAddress(wire)
	if err != nil || got != want || want != "[a:b:c]:80" {
		t.Errorf("DecodeAddress = (%q, %v), ReadAddress = %q", got, err, want)
	}
}
