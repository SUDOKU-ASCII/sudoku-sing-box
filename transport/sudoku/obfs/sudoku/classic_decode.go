package sudoku

// decodeWindow bounds how many wire bytes decode compacts at a time.
const decodeWindow = 256

// decode writes only as much plaintext as fits. It consumes whole windows of
// wire bytes, keeping hints of an unfinished group in hintBuf, but never more
// than can complete into the space left in p.
func (sc *Conn) decode(p, chunk []byte) (outN, consumed int, err error) {
	table := sc.table
	hintIdx := &table.layout.hintIdx
	var hints [512]uint8

	for consumed < len(chunk) && outN < len(p) {
		window := min(len(chunk)-consumed, decodeWindow, 4*(len(p)-outN)+3-sc.hintCount)
		k := copy(hints[:], sc.hintBuf[:sc.hintCount])
		for _, b := range chunk[consumed : consumed+window] {
			idx := hintIdx[b]
			hints[k&511] = idx
			k += int(idx>>7) ^ 1
		}
		consumed += window

		groups := k / 4
		for i := 0; i < groups; i++ {
			h := (*[4]uint8)(hints[4*i:])
			val, ok := table.decodeHints(h[0], h[1], h[2], h[3])
			if !ok {
				sc.hintCount = 0
				return outN, consumed, ErrInvalidSudokuMapMiss
			}
			p[outN] = val
			outN++
		}
		sc.hintCount = copy(sc.hintBuf[:], hints[4*groups:k])
	}
	return outN, consumed, nil
}
