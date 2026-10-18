package sudoku

import (
	"io"
	"math"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const maxEncodedWriteSize = 32 * 1024

// writeSudokuPayload bounds retained storage even for arbitrarily large Writes.
// Fill the remaining space with complete payload bytes, then send that buffer.
// Padding at the end belongs to the caller's Write, never to an internal batch.
func writeSudokuPayload(w io.Writer, buf []byte, table *Table, rng *sudokuRand, threshold uint64, p []byte) ([]byte, int, error) {
	maxExpansion := maxPaddedExpansion
	if threshold == 0 {
		maxExpansion = 4
	}
	needed := min(min(len(p), maxEncodedWriteSize)*maxExpansion+encodeSlack, maxEncodedWriteSize)
	buf = growWriteBuf(buf, needed)
	out := buf[:0]
	total, buffered := 0, 0
	for len(p) > 0 {
		n := min(len(p), (cap(out)-len(out)-encodeSlack)/maxExpansion)
		if n == 0 {
			if err := writeEncoded(w, out); err != nil {
				return releaseWriteBuf(out), total, err
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
	if threshold != 0 {
		draw := rng.Uint64()
		if draw&math.MaxUint32 < threshold {
			pool := table.layout.paddingPool
			out = append(out, pool[(draw>>32)*uint64(len(pool))>>32])
		}
	}
	if err := writeEncoded(w, out); err != nil {
		return releaseWriteBuf(out), total, err
	}
	return releaseWriteBuf(out), total + buffered, nil
}

// retainedWriteBytes is the encode buffer a connection keeps between writes.
// Larger encodes borrow a pooled buffer for the duration of one Write.
const retainedWriteBytes = 2048

// growWriteBuf returns an empty buffer with room for need bytes, borrowed from
// bufferPool when need exceeds what a connection may retain.
func growWriteBuf(buf []byte, need int) []byte {
	if cap(buf) >= need {
		return buf[:0]
	}
	if need <= retainedWriteBytes {
		return make([]byte, 0, need)
	}
	return getBuffer()[:0]
}

// releaseWriteBuf keeps a small buffer for the next Write and returns a pooled
// one.
func releaseWriteBuf(buf []byte) []byte {
	if cap(buf) <= retainedWriteBytes {
		return buf[:0]
	}
	putBuffer(buf)
	return nil
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
