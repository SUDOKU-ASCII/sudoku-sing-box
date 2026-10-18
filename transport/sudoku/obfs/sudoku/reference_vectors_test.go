package sudoku

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"testing"
	"testing/iotest"
)

// The vectors in testdata/v051_vectors.json were produced once by the v0.5.1
// release with a throwaway generator. They pin what every released peer puts on
// the wire, so this file is the contract the encoder and decoder are held to
// when internals change. Do not regenerate them from the current tree.

//go:embed testdata/v051_vectors.json
var referenceVectorsJSON []byte

type referenceTable struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Mode        string `json:"mode"`
	Custom      string `json:"custom"`
	Fingerprint string `json:"fingerprint"`
	Opposite    string `json:"opposite"`
	Hint        uint32 `json:"hint"`
}

type referenceStream struct {
	Name    string `json:"name"`
	Codec   string `json:"codec"`
	Key     string `json:"key"`
	Mode    string `json:"mode"`
	Custom  string `json:"custom"`
	Side    string `json:"side"`
	Padding int    `json:"padding"`
	Writes  []int  `json:"writes"`
	Wire    string `json:"wire"`
}

type referenceVectors struct {
	Tables  []referenceTable  `json:"tables"`
	Streams []referenceStream `json:"streams"`
}

func loadReferenceVectors(t testing.TB) referenceVectors {
	t.Helper()
	var v referenceVectors
	if err := json.Unmarshal(referenceVectorsJSON, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func referencePlaintext(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*131 + 7)
	}
	return p
}

// sortedKey packs four hint bytes, sorted ascending, into one big-endian word.
func sortedKey(h [4]byte) uint32 {
	s := h[:]
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return binary.BigEndian.Uint32(s)
}

// decodeEntries enumerates every four-cell hint set the decoder accepts, keyed
// like the v0.5.1 decode map: sorted wire bytes to plaintext byte.
func decodeEntries(tb *Table) map[uint32]byte {
	wire := func(idx int) byte { return tb.layout.encodeHint[idx&3][idx>>2] }
	entries := map[uint32]byte{}
	for a := 0; a < 64; a++ {
		for b := a + 1; b < 64; b++ {
			for c := b + 1; c < 64; c++ {
				for d := c + 1; d < 64; d++ {
					if v, ok := tb.decodeHints(uint8(a), uint8(b), uint8(c), uint8(d)); ok {
						entries[sortedKey([4]byte{wire(a), wire(b), wire(c), wire(d)})] = v
					}
				}
			}
		}
	}
	return entries
}

// tableFingerprint hashes the wire-visible content of a table in canonical
// order: the puzzle set per byte, the decode map, and the padding pool.
func tableFingerprint(tb *Table) string {
	h := sha256.New()
	var u32 [4]byte
	put := func(v uint32) {
		binary.BigEndian.PutUint32(u32[:], v)
		h.Write(u32[:])
	}
	for b := 0; b < 256; b++ {
		puzzles := puzzlesOf(tb, byte(b))
		keys := make([]uint32, 0, len(puzzles))
		for _, p := range puzzles {
			keys = append(keys, sortedKey(p))
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		put(uint32(len(keys)))
		for _, k := range keys {
			put(k)
		}
	}
	decode := decodeEntries(tb)
	decodeKeys := make([]uint32, 0, len(decode))
	for k := range decode {
		decodeKeys = append(decodeKeys, k)
	}
	sort.Slice(decodeKeys, func(i, j int) bool { return decodeKeys[i] < decodeKeys[j] })
	put(uint32(len(decodeKeys)))
	for _, k := range decodeKeys {
		put(k)
		h.Write([]byte{decode[k]})
	}
	pool := append([]byte(nil), tb.layout.paddingPool...)
	sort.Slice(pool, func(i, j int) bool { return pool[i] < pool[j] })
	put(uint32(len(pool)))
	h.Write(pool)
	if tb.IsASCII {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestReferenceTableFingerprints(t *testing.T) {
	for _, want := range loadReferenceVectors(t).Tables {
		t.Run(want.Name, func(t *testing.T) {
			tb, err := NewTableWithCustom(want.Key, want.Mode, want.Custom)
			if err != nil {
				t.Fatal(err)
			}
			if got := tableFingerprint(tb); got != want.Fingerprint {
				t.Fatalf("table content changed:\n got %s\nwant %s", got, want.Fingerprint)
			}
			if got := tb.Hint(); got != want.Hint {
				t.Fatalf("table hint = %d, want %d", got, want.Hint)
			}
			opposite := tb.OppositeDirection()
			if want.Opposite == "" {
				if opposite != tb {
					t.Fatal("symmetric mode must be its own opposite")
				}
				return
			}
			if got := tableFingerprint(opposite); got != want.Opposite {
				t.Fatalf("opposite table content changed:\n got %s\nwant %s", got, want.Opposite)
			}
			if opposite.Hint() != want.Hint {
				t.Fatalf("opposite hint = %d, want %d", opposite.Hint(), want.Hint)
			}
		})
	}
}

func TestReferenceStreamsDecode(t *testing.T) {
	vectors := loadReferenceVectors(t)
	tables := map[string]*Table{}
	tableFor := func(s referenceStream) *Table {
		id := s.Key + "|" + s.Mode + "|" + s.Custom
		tb := tables[id]
		if tb == nil {
			var err error
			if tb, err = NewTableWithCustom(s.Key, s.Mode, s.Custom); err != nil {
				t.Fatal(err)
			}
			tables[id] = tb
		}
		if s.Side == "down" {
			return tb.OppositeDirection()
		}
		return tb
	}

	for _, s := range vectors.Streams {
		wire, err := hex.DecodeString(s.Wire)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, n := range s.Writes {
			total += n
		}
		want := referencePlaintext(total)
		tb := tableFor(s)

		decode := func(t *testing.T, fragment int, oneByteReads bool) ([]byte, error) {
			raw := &fragmentConn{data: wire, limit: fragment}
			var r io.Reader
			switch s.Codec {
			case "pure":
				r = NewConn(raw, tb, s.Padding, s.Padding, false)
			case "packed":
				r = NewPackedConn(raw, tb, s.Padding, s.Padding)
			default:
				t.Fatalf("unknown codec %q", s.Codec)
			}
			if oneByteReads {
				r = iotest.OneByteReader(r)
			}
			return io.ReadAll(r)
		}

		t.Run(s.Name, func(t *testing.T) {
			for _, fragment := range []int{1, 7, 127, 4096, len(wire)} {
				for _, oneByteReads := range []bool{false, true} {
					got, err := decode(t, fragment, oneByteReads)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("fragment=%d oneByteReads=%v: decoded %d bytes (want %d), err=%v",
							fragment, oneByteReads, len(got), len(want), err)
					}
				}
			}
		})
	}
}
