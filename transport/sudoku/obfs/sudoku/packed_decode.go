package sudoku

// decode writes only as much plaintext as fits; consumed wire bytes can then be discarded.
func (pc *PackedConn) decode(p, chunk []byte) (outN, consumed int, err error) {
	// Cache frequently accessed variables
	rBuf := pc.readBitBuf
	rBits := pc.readBits
	padMarker := pc.padMarker
	layout := pc.table.layout

	for consumed < len(chunk) && outN < len(p) {
		i := consumed
		if rBits == 0 && outN+3 <= len(p) && i+3 < len(chunk) {
			g1 := layout.decodeGroup[chunk[i]]
			g2 := layout.decodeGroup[chunk[i+1]]
			g3 := layout.decodeGroup[chunk[i+2]]
			g4 := layout.decodeGroup[chunk[i+3]]
			// The invalid sentinel combines validation and decoding in one lookup.
			if (g1 | g2 | g3 | g4) < 64 {
				p[outN] = (g1 << 2) | (g2 >> 4)
				p[outN+1] = (g2 << 4) | (g3 >> 2)
				p[outN+2] = (g3 << 6) | g4
				outN += 3
				consumed += 4
				continue
			}
		}

		b := chunk[i]
		consumed++
		group := layout.decodeGroup[b]
		if group >= 64 {
			if b == padMarker {
				rBuf = 0
				rBits = 0
			}
			continue
		}

		rBuf = (rBuf << 6) | uint64(group)
		rBits += 6

		if rBits >= 8 {
			rBits -= 8
			val := byte(rBuf >> rBits)
			p[outN] = val
			outN++
			if rBits == 0 {
				rBuf = 0
			} else {
				rBuf &= (uint64(1) << rBits) - 1
			}
		}
	}

	pc.readBitBuf = rBuf
	pc.readBits = rBits
	return outN, consumed, nil
}
