package sudoku

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"math/bits"
	"math/rand/v2"
	"sort"
	"testing"
)

// These tests pin the statistical shape of pure-mode output, not its exact
// bytes: padding rate and placement, which puzzle and hint order are chosen,
// the byte histogram, its Hamming weight and its ASCII ratio. An encoder rewrite
// may change RNG draw order freely as long as these keep holding. Encoding is
// randomly seeded, so every bound is a 6-sigma bound.

const statPlaintextLen = 1 << 18

func puzzleSetKey(h [4]byte) uint32 {
	s := h
	sort.Slice(s[:], func(i, j int) bool { return s[i] < s[j] })
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

func encodePure(t *testing.T, table *Table, pct int, plain []byte) []byte {
	t.Helper()
	var wire bytes.Buffer
	conn := NewConn(writeOnlyConn{Writer: &wire}, table, pct, pct, false)
	if n, err := conn.Write(plain); err != nil || n != len(plain) {
		t.Fatalf("write = (%d, %v)", n, err)
	}
	return wire.Bytes()
}

func chiSquareLimit(dof int) float64 {
	return float64(dof) + 6*math.Sqrt(2*float64(dof)) + 6
}

func requireUniform(t *testing.T, what string, counts []int) {
	t.Helper()
	total := 0
	for _, c := range counts {
		total += c
	}
	expected := float64(total) / float64(len(counts))
	stat := 0.0
	for _, c := range counts {
		d := float64(c) - expected
		stat += d * d / expected
	}
	if limit := chiSquareLimit(len(counts) - 1); stat > limit {
		t.Fatalf("%s is not uniform: chi2=%.1f over %d cells, limit %.1f", what, stat, len(counts), limit)
	}
}

func requireBinomial(t *testing.T, what string, got int, n int, p float64) {
	t.Helper()
	want := float64(n) * p
	tol := 6*math.Sqrt(float64(n)*p*(1-p)) + 2
	if math.Abs(float64(got)-want) > tol {
		t.Fatalf("%s = %d, want %.0f +/- %.0f", what, got, want, tol)
	}
}

func TestPureOutputStatistics(t *testing.T) {
	layouts := []struct{ name, mode, custom string }{
		{"ascii", "prefer_ascii", ""},
		{"entropy", "prefer_entropy", ""},
		{"custom", "prefer_entropy", "xppxvvvv"},
	}
	for _, l := range layouts {
		table, err := NewTableWithCustom("stats-key-"+l.name, l.mode, l.custom)
		if err != nil {
			t.Fatal(err)
		}
		for _, pct := range []int{0, 10, 100} {
			t.Run(fmt.Sprintf("%s/pad%d", l.name, pct), func(t *testing.T) {
				checkWireStatistics(t, table, pct)
				checkPuzzleAndOrderUniformity(t, table, pct)
			})
		}
	}
}

func checkWireStatistics(t *testing.T, table *Table, pct int) {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	plain := make([]byte, statPlaintextLen)
	for i := range plain {
		plain[i] = byte(rng.Uint32())
	}
	wire := encodePure(t, table, pct, plain)
	p := float64(pct) / 100

	got, err := io.ReadAll(NewConn(&memConn{readBuf: wire}, table, pct, pct, false))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip failed: %d bytes back, err=%v", len(got), err)
	}

	var inPool [256]bool
	for _, b := range table.layout.paddingPool {
		inPool[b] = true
	}

	// A group is up to five padding slots (one before the puzzle, one before each
	// hint) and four hints, so padding lands only between hints or at group start,
	// where the first two slots can touch.
	var hist [256]int
	var padsAfterHints [4]int
	hints, inGroup, run := 0, 0, 0
	for _, b := range wire {
		hist[b]++
		if isHint(table.layout, b) {
			hints++
			inGroup = (inGroup + 1) % 4
			run = 0
			continue
		}
		if !inPool[b] {
			t.Fatalf("byte %#02x is neither a hint nor padding", b)
		}
		padsAfterHints[inGroup]++
		if run++; run > 2 || (run == 2 && inGroup != 0) {
			t.Fatalf("padding run of %d after %d hints of a group", run, inGroup)
		}
	}

	if hints != 4*statPlaintextLen {
		t.Fatalf("hints = %d, want %d", hints, 4*statPlaintextLen)
	}
	switch pct {
	case 0:
		if len(wire) != hints {
			t.Fatalf("wire has %d bytes, want %d with padding off", len(wire), hints)
		}
	case 100:
		if padsAfterHints[0] != 2*statPlaintextLen+1 || padsAfterHints[1] != statPlaintextLen ||
			padsAfterHints[2] != statPlaintextLen || padsAfterHints[3] != statPlaintextLen {
			t.Fatalf("full padding layout wrong: %v", padsAfterHints)
		}
	default:
		requireBinomial(t, "padding at group start", padsAfterHints[0], 2*statPlaintextLen, p)
		for k := 1; k < 4; k++ {
			requireBinomial(t, fmt.Sprintf("padding after %d hints", k), padsAfterHints[k], statPlaintextLen, p)
		}
	}

	// Expected histogram: uniform plaintext, uniform puzzle, uniform order, then
	// padding drawn uniformly from the pool.
	var expected [256]float64
	for b := 0; b < 256; b++ {
		puzzles := puzzlesOf(table, byte(b))
		for _, puzzle := range puzzles {
			for _, h := range puzzle {
				expected[h] += 1 / 256.0 / float64(len(puzzles)) / 4
			}
		}
	}
	hintShare := 4 / (4 + 5*p)
	pool := table.layout.paddingPool
	for i := range expected {
		expected[i] *= hintShare
	}
	for _, b := range pool {
		expected[b] += (1 - hintShare) / float64(len(pool))
	}

	total := float64(len(wire))
	tv, sigma, wantWeight, gotWeight, wantASCII, gotASCII := 0.0, 0.0, 0.0, 0.0, 0.0, 0.0
	for i := 0; i < 256; i++ {
		measured := float64(hist[i]) / total
		tv += math.Abs(measured-expected[i]) / 2
		sigma += math.Sqrt(expected[i] * (1 - expected[i]) / total)
		wantWeight += expected[i] * float64(bits.OnesCount8(uint8(i)))
		gotWeight += measured * float64(bits.OnesCount8(uint8(i)))
		if i < 0x80 {
			wantASCII += expected[i]
			gotASCII += measured
		}
	}
	if tv > 0.6*sigma {
		t.Fatalf("byte histogram total variation %.5f exceeds %.5f", tv, 0.6*sigma)
	}
	if math.Abs(gotWeight-wantWeight) > 0.01 {
		t.Fatalf("mean Hamming weight %.4f, want %.4f", gotWeight, wantWeight)
	}
	if math.Abs(gotASCII-wantASCII) > 0.01 {
		t.Fatalf("ASCII ratio %.4f, want %.4f", gotASCII, wantASCII)
	}
	if table.IsASCII && gotASCII < 1-1e-9 {
		t.Fatalf("ASCII layout produced non-ASCII bytes (ratio %.4f)", gotASCII)
	}

	if pct > 0 {
		counts := make([]int, len(pool))
		for i, b := range pool {
			counts[i] = hist[b]
		}
		requireUniform(t, "padding byte choice", counts)
	}
}

// checkPuzzleAndOrderUniformity encodes one plaintext byte many times and
// checks every puzzle of that byte, and every hint order, is equally likely.
func checkPuzzleAndOrderUniformity(t *testing.T, table *Table, pct int) {
	t.Helper()
	const target = 0x5A
	plain := bytes.Repeat([]byte{target}, statPlaintextLen/2)
	wire := encodePure(t, table, pct, plain)

	puzzles := puzzlesOf(table, target)
	index := make(map[uint32]int, len(puzzles))
	for i, puzzle := range puzzles {
		index[puzzleSetKey(puzzle)] = i
	}
	puzzleCounts := make([]int, len(puzzles))
	orderCounts := map[int]int{}

	var group [4]byte
	n := 0
	for _, b := range wire {
		if !isHint(table.layout, b) {
			continue
		}
		group[n] = b
		if n++; n < 4 {
			continue
		}
		n = 0
		i, ok := index[puzzleSetKey(group)]
		if !ok {
			t.Fatalf("hint group %v is not a puzzle of byte %#02x", group, target)
		}
		puzzleCounts[i]++
		order := 0
		for _, h := range group {
			pos := bytes.IndexByte(puzzles[i][:], h)
			order = order*4 + pos
		}
		orderCounts[order]++
	}

	requireUniform(t, "puzzle choice", puzzleCounts)
	if len(orderCounts) != 24 {
		t.Fatalf("saw %d distinct hint orders, want 24", len(orderCounts))
	}
	orders := make([]int, 0, 24)
	for _, c := range orderCounts {
		orders = append(orders, c)
	}
	requireUniform(t, "hint order", orders)
}
