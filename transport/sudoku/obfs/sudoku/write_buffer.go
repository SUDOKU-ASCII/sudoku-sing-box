package sudoku

import (
	"io"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const maxEncodedWriteSize = 32 * 1024

// writeSudokuPayload bounds retained storage even for arbitrarily large Writes.
// Fill the remaining space with complete payload bytes, then send that buffer.
// Padding at the end belongs to the caller's Write, never to an internal batch.
func writeSudokuPayload(w io.Writer, buf []byte, table *Table, rng *sudokuRand, threshold uint64, p []byte) ([]byte, int, error) {
	maxExpansion := 9 // one leading pad and four (pad, hint) pairs
	if threshold == 0 {
		maxExpansion = 4
	}
	needed := min(min(len(p), maxEncodedWriteSize)*maxExpansion+1, maxEncodedWriteSize)
	if cap(buf) < needed {
		buf = make([]byte, 0, needed)
	}
	out := buf[:0]
	total, buffered := 0, 0
	for len(p) > 0 {
		n := min(len(p), (cap(out)-len(out)-1)/maxExpansion)
		if n == 0 {
			if err := writeEncoded(w, out); err != nil {
				return out[:0], total, err
			}
			total += buffered
			buffered = 0
			out = out[:0]
			continue
		}
		out = appendSudokuPayload(out, table, rng, threshold, p[:n])
		buffered += n
		p = p[n:]
	}
	if threshold >= probOne || (threshold != 0 && uint64(rng.Uint32()) < threshold) {
		out = append(out, table.PaddingPool[rng.Intn(len(table.PaddingPool))])
	}
	if err := writeEncoded(w, out); err != nil {
		return out[:0], total, err
	}
	return out[:0], total + buffered, nil
}

// Keep transport writes near the decoder's read-buffer size. Larger writes can
// stall TCP forwarding on Windows even when the codec itself is faster. These
// are slices of the final encoded buffer: no copying, padding or new framing.
func writeEncoded(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n := min(len(p), maxEncodedWriteSize)
		if err := connutil.WriteFull(w, p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}
