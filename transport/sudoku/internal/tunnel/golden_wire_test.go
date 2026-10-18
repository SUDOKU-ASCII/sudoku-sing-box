package tunnel

import (
	"bytes"
	"encoding/hex"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// The golden vectors in golden_vectors_test.go were produced by the v0.5.1 release. Every
// test here pins this tree to those bytes in both directions: what we send must equal what
// the release sent, and what the release sent must decode.

func goldenBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func goldenSeq(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func goldenHellos() (*KIPClientHello, *KIPClientHello, *KIPServerHello) {
	var userHash [kipHelloUserHashSize]byte
	var nonce [kipHelloNonceSize]byte
	var pub [kipHelloPubSize]byte
	copy(userHash[:], goldenSeq(0x01, 8))
	copy(nonce[:], goldenSeq(0x10, 16))
	copy(pub[:], goldenSeq(0x20, 32))
	ts := time.Unix(1700000000, 0)
	return &KIPClientHello{Timestamp: ts, UserHash: userHash, Nonce: nonce, ClientPub: pub, Features: KIPFeatAll},
		&KIPClientHello{Timestamp: ts, UserHash: userHash, Nonce: nonce, ClientPub: pub, Features: KIPFeatOpenTCP | KIPFeatMux, TableHint: 0xdeadbeef, HasTableHint: true},
		&KIPServerHello{Nonce: nonce, ServerPub: pub, SelectedFeats: KIPFeatAll}
}

func TestGoldenKIPMessages(t *testing.T) {
	ch, chHint, sh := goldenHellos()

	encode := func(typ byte, payload []byte) string {
		var b bytes.Buffer
		if err := WriteKIPMessage(&b, typ, payload); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b.Bytes())
	}
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"client hello", encode(KIPTypeClientHello, ch.EncodePayload()), goldenKIPClientHello},
		{"client hello with table hint", encode(KIPTypeClientHello, chHint.EncodePayload()), goldenKIPClientHelloWithHint},
		{"server hello", encode(KIPTypeServerHello, sh.EncodePayload()), goldenKIPServerHello},
		{"start mux", encode(KIPTypeStartMux, nil), goldenKIPStartMux},
		{"start uot", encode(KIPTypeStartUoT, nil), goldenKIPStartUoT},
		{"start rev", encode(KIPTypeStartRev, nil), goldenKIPStartRev},
		{"keepalive", encode(KIPTypeKeepAlive, nil), goldenKIPKeepAlive},
	} {
		if c.got != c.want {
			t.Errorf("%s: wire = %s, want %s", c.name, c.got, c.want)
		}
	}

	var open bytes.Buffer
	if err := writeKIPOpenTCP(&open, "example.com:443"); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(open.Bytes()); got != goldenKIPOpenTCP {
		t.Errorf("open tcp: wire = %s, want %s", got, goldenKIPOpenTCP)
	}
}

func TestGoldenKIPDecode(t *testing.T) {
	ch, chHint, sh := goldenHellos()

	read := func(wire string, wantType byte) []byte {
		msg, err := ReadKIPMessage(bytes.NewReader(goldenBytes(t, wire)))
		if err != nil {
			t.Fatalf("ReadKIPMessage: %v", err)
		}
		if msg.Type != wantType {
			t.Fatalf("type = 0x%02x, want 0x%02x", msg.Type, wantType)
		}
		return msg.Payload
	}

	for _, c := range []struct {
		name string
		wire string
		want *KIPClientHello
	}{
		{"client hello", goldenKIPClientHello, ch},
		{"client hello with table hint", goldenKIPClientHelloWithHint, chHint},
	} {
		got, err := DecodeKIPClientHelloPayload(read(c.wire, KIPTypeClientHello))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !got.Timestamp.Equal(c.want.Timestamp) || got.UserHash != c.want.UserHash || got.Nonce != c.want.Nonce ||
			got.ClientPub != c.want.ClientPub || got.Features != c.want.Features ||
			got.HasTableHint != c.want.HasTableHint || got.TableHint != c.want.TableHint {
			t.Errorf("%s: decoded %+v, want %+v", c.name, got, c.want)
		}
	}

	gotSH, err := DecodeKIPServerHelloPayload(read(goldenKIPServerHello, KIPTypeServerHello))
	if err != nil {
		t.Fatal(err)
	}
	if *gotSH != *sh {
		t.Errorf("server hello: decoded %+v, want %+v", gotSH, sh)
	}

	for wire, typ := range map[string]byte{
		goldenKIPStartMux:  KIPTypeStartMux,
		goldenKIPStartUoT:  KIPTypeStartUoT,
		goldenKIPStartRev:  KIPTypeStartRev,
		goldenKIPKeepAlive: KIPTypeKeepAlive,
	} {
		if p := read(wire, typ); len(p) != 0 {
			t.Errorf("type 0x%02x carries %d payload bytes, want none", typ, len(p))
		}
	}
	if p := read(goldenKIPOpenTCP, KIPTypeOpenTCP); hex.EncodeToString(p) != "030b6578616d706c652e636f6d01bb" {
		t.Errorf("open tcp payload = %x", p)
	}
}

func TestGoldenSessionKeys(t *testing.T) {
	var nonce [kipHelloNonceSize]byte
	copy(nonce[:], goldenSeq(0x10, 16))

	c2s, s2c := derivePSKDirectionalBases("golden-psk")
	if hex.EncodeToString(c2s) != goldenPSKC2S || hex.EncodeToString(s2c) != goldenPSKS2C {
		t.Errorf("psk bases = %x / %x", c2s, s2c)
	}
	c2s, s2c, err := deriveSessionDirectionalBases("golden-psk", goldenSeq(0x40, 32), nonce)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(c2s) != goldenSessionC2S || hex.EncodeToString(s2c) != goldenSessionS2C {
		t.Errorf("session bases = %x / %x", c2s, s2c)
	}
}

// captureMuxWrites runs fn against a session and returns everything the session wrote.
func captureMuxWrites(t *testing.T, fn func(s *muxSession)) []byte {
	t.Helper()
	a, b := net.Pipe()
	s := newMuxSession(a, nil)
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, b); close(done) }()
	fn(s)
	s.closeWithError(io.ErrClosedPipe)
	<-done
	return buf.Bytes()
}

func goldenMuxWire(t *testing.T, name string) []byte {
	t.Helper()
	for _, f := range goldenMuxFrames {
		if f.name == name {
			return goldenBytes(t, f.wire)
		}
	}
	t.Fatalf("no golden mux frame %q", name)
	return nil
}

func TestGoldenMuxFramesEncode(t *testing.T) {
	for _, f := range goldenMuxFrames {
		got := captureMuxWrites(t, func(s *muxSession) {
			if err := s.sendFrame(f.typ, f.id, goldenBytes(t, f.payload)); err != nil {
				t.Fatal(err)
			}
		})
		if hex.EncodeToString(got) != f.wire {
			t.Errorf("%s: wire = %x, want %s", f.name, got, f.wire)
		}
	}
	got := captureMuxWrites(t, func(s *muxSession) { s.sendReset(3, "bad address") })
	if hex.EncodeToString(got) != goldenMuxSendReset {
		t.Errorf("sendReset: wire = %x, want %s", got, goldenMuxSendReset)
	}
}

// serveGoldenMux plays reference frames into a session whose onOpen reports the opened stream.
func serveGoldenMux(t *testing.T) (peer net.Conn, opened chan *muxStream, openPayload chan []byte) {
	t.Helper()
	a, b := net.Pipe()
	opened = make(chan *muxStream, 4)
	openPayload = make(chan []byte, 4)
	s := newMuxSession(a, func(st *muxStream, payload []byte) {
		openPayload <- payload
		opened <- st
	})
	t.Cleanup(func() {
		s.closeWithError(io.ErrClosedPipe)
		_ = b.Close()
	})
	return b, opened, openPayload
}

func TestGoldenMuxFramesDecode(t *testing.T) {
	t.Run("open data close ping", func(t *testing.T) {
		peer, opened, openPayload := serveGoldenMux(t)
		script := bytes.Join([][]byte{
			goldenMuxWire(t, "open"),
			goldenMuxWire(t, "data"),
			goldenMuxWire(t, "data_keepalive"),
			goldenMuxWire(t, "data"),
			goldenMuxWire(t, "close"),
			goldenMuxWire(t, "ping"),
		}, nil)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = peer.Write(script) }()

		_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
		pong := make([]byte, muxHeaderSize)
		if _, err := io.ReadFull(peer, pong); err != nil {
			t.Fatalf("read pong: %v", err)
		}
		if !bytes.Equal(pong, goldenMuxWire(t, "pong")) {
			t.Errorf("pong = %x, want %x", pong, goldenMuxWire(t, "pong"))
		}
		wg.Wait()

		if got := hex.EncodeToString(<-openPayload); got != "030b6578616d706c652e636f6d01bb" {
			t.Errorf("open payload = %s", got)
		}
		st := <-opened
		got, err := io.ReadAll(st)
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		if string(got) != "hellohello" {
			t.Errorf("stream data = %q, want %q", got, "hellohello")
		}
	})

	t.Run("open reset", func(t *testing.T) {
		peer, opened, openPayload := serveGoldenMux(t)
		go func() {
			_, _ = peer.Write(bytes.Join([][]byte{goldenMuxWire(t, "open_ipv4"), goldenMuxWire(t, "reset")}, nil))
		}()
		if got := hex.EncodeToString(<-openPayload); got != "01010203041f90" {
			t.Errorf("open payload = %s", got)
		}
		st := <-opened
		_ = st.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 8)
		_, err := st.Read(buf)
		if err == nil || err.Error() != "boom" {
			t.Errorf("read after reset: err = %v, want boom", err)
		}
	})
}

// syncBuffer is a concurrency-safe sink that can be polled for expected length.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) waitFor(n int, timeout time.Duration) []byte {
	deadline := time.Now().Add(timeout)
	for {
		b.mu.Lock()
		got := append([]byte(nil), b.buf.Bytes()...)
		b.mu.Unlock()
		if len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGoldenUoTFrames(t *testing.T) {
	for _, f := range goldenUoTFrames {
		wire := goldenBytes(t, f.wire)
		payload := goldenBytes(t, f.payload)

		host, got, err := ReadUoTDatagram(bytes.NewReader(wire))
		if err != nil {
			t.Fatalf("ReadUoTDatagram(%s): %v", f.addr, err)
		}
		if host != f.decoded || !bytes.Equal(got, payload) {
			t.Errorf("ReadUoTDatagram(%s) = (%q, %x), want (%q, %x)", f.addr, host, got, f.decoded, payload)
		}

		var sink syncBuffer
		w := NewUoTWriter(&sink, nil)
		if err := w.WriteDatagram(f.addr, payload); err != nil {
			t.Fatalf("WriteDatagram(%s): %v", f.addr, err)
		}
		out := sink.waitFor(len(wire), 2*time.Second)
		w.Close()
		if !bytes.Equal(out, wire) {
			t.Errorf("UoTWriter(%s) wrote %x, want %x", f.addr, out, wire)
		}
	}
}

// A peer may encode an IPv4-mapped destination as ATYP 4; the release reported it as dotted IPv4.
func TestUoTDecodeMappedIPv6AsIPv4(t *testing.T) {
	wire := goldenBytes(t, "00130001"+"04"+"00000000000000000000ffff"+"09090909"+"14e9"+"6d")
	host, payload, err := ReadUoTDatagram(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if host != "9.9.9.9:5353" || string(payload) != "m" {
		t.Errorf("got (%q, %q), want (9.9.9.9:5353, m)", host, payload)
	}
}

type writeCounter struct{ writes int }

func (c *writeCounter) Write(p []byte) (int, error) { c.writes++; return len(p), nil }

// Each KIP message must reach the transport as one write so it is framed as one record.
func TestKIPMessagesAreSingleWrite(t *testing.T) {
	var c writeCounter
	if err := WriteKIPMessage(&c, KIPTypeServerHello, make([]byte, 52)); err != nil {
		t.Fatal(err)
	}
	if err := WriteKIPMessage(&c, KIPTypeStartMux, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeKIPOpenTCP(&c, "example.com:443"); err != nil {
		t.Fatal(err)
	}
	if c.writes != 3 {
		t.Errorf("3 messages took %d writes", c.writes)
	}
}
