package sudoku

import "bufio"

// peekBufferedChunk borrows available wire bytes without a second raw buffer.
// Peek(1) fills an empty reader but never waits for a whole requested chunk.
// The returned slice is valid only until the next reader operation.
// An error is returned only when there are no bytes to decode.
// The caller discards only the bytes it decoded, keeping the rest for the next Read.
func peekBufferedChunk(reader *bufio.Reader) ([]byte, error) {
	if reader.Buffered() == 0 {
		if _, err := reader.Peek(1); err != nil {
			return nil, err
		}
	}
	chunk, _ := reader.Peek(reader.Buffered())
	return chunk, nil
}
