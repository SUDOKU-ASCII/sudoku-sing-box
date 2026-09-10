package sudoku

// decode stops at the caller's capacity, leaving unconsumed wire bytes buffered.
func (pc *PackedConn) decode(p, chunk []byte) (outN, consumed int, err error) {
	plainLen, wireLen := len(p), len(chunk)
	rBuf, rBits := pc.readBitBuf, pc.readBits
	padMarker := pc.padMarker
	groups := &pc.table.layout.decodeGroup
	for len(chunk) > 0 && len(p) > 0 {
		if rBits == 0 && len(p) >= 3 && len(chunk) >= 4 {
			g0, g1, g2, g3 := groups[chunk[0]], groups[chunk[1]], groups[chunk[2]], groups[chunk[3]]
			if (g0 | g1 | g2 | g3) < 64 {
				p[0], p[1], p[2] = g0<<2|g1>>4, g1<<4|g2>>2, g2<<6|g3
				p, chunk = p[3:], chunk[4:]
				continue
			}
		}
		b := chunk[0]
		chunk = chunk[1:]
		group := groups[b]
		if group >= 64 {
			if b == padMarker {
				rBuf, rBits = 0, 0
			}
			continue
		}
		rBuf = rBuf<<6 | uint64(group)
		rBits += 6
		if rBits >= 8 {
			rBits -= 8
			p[0] = byte(rBuf >> rBits)
			p = p[1:]
			rBuf &= (uint64(1) << rBits) - 1
		}
	}
	pc.readBitBuf, pc.readBits = rBuf, rBits
	return plainLen - len(p), wireLen - len(chunk), nil
}
