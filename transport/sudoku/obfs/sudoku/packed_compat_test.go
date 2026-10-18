package sudoku

import (
	"bytes"
	"io"
	"testing"
)

func FuzzPackedReadCompatibility(f *testing.F) {
	custom, err := newCustomLayout("xvpvvpxv")
	if err != nil {
		f.Fatal(err)
	}
	layouts := []*byteLayout{newASCIILayout(), newEntropyLayout(), custom}
	f.Add([]byte{0x40, 0x41, 0x42, 0x43, 0x3f, '\n', 0xff}, uint8(0), uint8(7))
	f.Add([]byte{0, 1, 2, 3, 0x80, 0x10, 0x6f}, uint8(1), uint8(1))
	f.Add([]byte{}, uint8(2), uint8(32))
	f.Fuzz(func(t *testing.T, wire []byte, mode, fragment uint8) {
		wire = wire[:min(len(wire), 4096)]
		layout := layouts[int(mode)%len(layouts)]
		want := decodePackedReference(layout, wire)
		raw := &fragmentConn{data: wire, limit: 1 + int(fragment)}
		reader := NewPackedConn(raw, &Table{layout: layout}, 0, 0)
		got, err := io.ReadAll(reader)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("packed decoder differs from scalar decoder: %v", err)
		}

		// Check the optimized encoder against that legacy decoder too, across
		// write boundaries, padding levels and every supported layout.
		var encoded bytes.Buffer
		padding := []int{0, 10, 100}[int(fragment)%3]
		table := &Table{layout: layout}
		writer := NewPackedConn(writeOnlyConn{Writer: &encoded}, table, padding, padding)
		writer.rng = newSudokuRand(1)
		split := len(wire) / 2
		for _, p := range [][]byte{wire[:split], wire[split:]} {
			if _, err := writer.Write(p); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(decodePackedReference(layout, encoded.Bytes()), wire) {
			t.Fatal("encoded data is incompatible with the legacy decoder")
		}
	})
}

// Independent scalar decoder preserving the original acceptance rules,
// including legacy ASCII aliases and markers between incomplete groups.
func decodePackedReference(layout *byteLayout, wire []byte) []byte {
	var out []byte
	var bits uint32
	count := 0
	for _, b := range wire {
		if !isHint(layout, b) {
			if b == layout.padMarker {
				bits, count = 0, 0
			}
			continue
		}
		bits = bits<<6 | uint32(layout.decodeGroup[b])
		count += 6
		if count >= 8 {
			count -= 8
			out = append(out, byte(bits>>count))
		}
	}
	return out
}
