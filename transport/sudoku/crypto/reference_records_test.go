package crypto

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"
)

// testdata/v051_records.json holds frames written by the v0.5.1 release with
// KeyUpdateAfterBytes=100, so each stream crosses several key epochs. They pin
// the record framing, nonce/AAD layout and per-epoch key derivation. Do not
// regenerate them from the current tree.

//go:embed testdata/v051_records.json
var referenceRecordsJSON []byte

type referenceRecordStream struct {
	Method   string `json:"method"`
	BaseKey  string `json:"base_key"`
	PlainLen int    `json:"plain_len"`
	Frames   int    `json:"frames"`
	Epochs   int    `json:"epochs"`
	Wire     string `json:"wire"`
}

func referencePlaintext(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*131 + 7)
	}
	return p
}

func TestReferenceRecordsDecode(t *testing.T) {
	var streams []referenceRecordStream
	if err := json.Unmarshal(referenceRecordsJSON, &streams); err != nil {
		t.Fatal(err)
	}
	unused := sha256.Sum256([]byte("unused send key"))
	for _, s := range streams {
		name := s.Method
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			wire, err := hex.DecodeString(s.Wire)
			if err != nil {
				t.Fatal(err)
			}
			base, err := hex.DecodeString(s.BaseKey)
			if err != nil {
				t.Fatal(err)
			}
			want := referencePlaintext(s.PlainLen)

			frames, epochs := 0, map[uint32]bool{}
			for p := 0; p < len(wire); frames++ {
				epochs[binary.BigEndian.Uint32(wire[p+2:])] = true
				p += 2 + int(binary.BigEndian.Uint16(wire[p:]))
			}
			if frames != s.Frames || len(epochs) != s.Epochs || s.Epochs < 2 {
				t.Fatalf("fixture shape: frames=%d epochs=%d, recorded frames=%d epochs=%d", frames, len(epochs), s.Frames, s.Epochs)
			}

			newReceiver := func() *RecordConn {
				rc, err := NewRecordConn(&replayConn{reader: bytes.NewReader(wire)}, s.Method, unused[:], base)
				if err != nil {
					t.Fatal(err)
				}
				return rc
			}

			for _, readSize := range []int{1, 7, 4096} {
				rc := newReceiver()
				var got []byte
				buf := make([]byte, readSize)
				for {
					n, err := rc.Read(buf)
					got = append(got, buf[:n]...)
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("readSize=%d: %v", readSize, err)
					}
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("readSize=%d: plaintext mismatch (%d bytes, want %d)", readSize, len(got), len(want))
				}
			}

			var copied bytes.Buffer
			if n, err := newReceiver().WriteTo(&copied); err != nil || n != int64(len(want)) || !bytes.Equal(copied.Bytes(), want) {
				t.Fatalf("WriteTo: n=%d err=%v", n, err)
			}
		})
	}
}
