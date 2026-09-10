package sudoku

func appendSudokuPayload(dst []byte, table *Table, rng *sudokuRand, paddingThreshold uint64, p []byte) []byte {
	if len(p) == 0 {
		return dst
	}
	if paddingThreshold == 0 {
		return appendSudokuPayloadNoPadding(dst, table, rng, p)
	}

	out := dst
	pads := table.PaddingPool
	padLen := len(pads)

	if paddingThreshold >= probOne {
		for _, b := range p {
			out = append(out, pads[rng.Intn(padLen)])

			puzzles := table.EncodeTable[b]
			puzzle := puzzles[rng.Intn(len(puzzles))]

			perm := perm4[rng.Intn(len(perm4))]
			for _, idx := range perm {
				out = append(out, pads[rng.Intn(padLen)], puzzle[idx])
			}
		}

		return out
	}

	for _, b := range p {
		if uint64(rng.Uint32()) < paddingThreshold {
			out = append(out, pads[rng.Intn(padLen)])
		}

		puzzles := table.EncodeTable[b]
		puzzle := puzzles[rng.Intn(len(puzzles))]

		perm := perm4[rng.Intn(len(perm4))]
		for _, idx := range perm {
			if uint64(rng.Uint32()) < paddingThreshold {
				out = append(out, pads[rng.Intn(padLen)])
			}
			out = append(out, puzzle[idx])
		}
	}

	return out
}

func appendSudokuPayloadNoPadding(dst []byte, table *Table, rng *sudokuRand, p []byte) []byte {
	out := dst

	for _, b := range p {
		puzzles := table.EncodeTable[b]
		puzzle := puzzles[rng.Intn(len(puzzles))]
		perm := perm4[rng.Intn(len(perm4))]
		out = append(out, puzzle[perm[0]], puzzle[perm[1]], puzzle[perm[2]], puzzle[perm[3]])
	}
	return out
}
