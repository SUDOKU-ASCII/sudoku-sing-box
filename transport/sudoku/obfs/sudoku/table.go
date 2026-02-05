package sudoku

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/rand"
)

var ErrInvalidSudokuMapMiss = errors.New("INVALID_SUDOKU_MAP_MISS")

type Table struct {
	EncodeTable [256][][4]byte
	DecodeMap   map[uint32]byte
	PaddingPool []byte
	IsASCII     bool
	layout      *byteLayout
}

func NewTableWithCustom(key string, mode string, customPattern string) (*Table, error) {
	layout, err := resolveLayout(mode, customPattern)
	if err != nil {
		return nil, err
	}

	t := &Table{
		DecodeMap: make(map[uint32]byte),
		IsASCII:   layout.name == "ascii",
		layout:    layout,
	}
	t.PaddingPool = append(t.PaddingPool, layout.paddingPool...)

	grids := allGrids()
	hash := sha256.Sum256([]byte(key))
	seed := int64(binary.BigEndian.Uint64(hash[:8]))
	rng := rand.New(rand.NewSource(seed))

	shuffledGrids := make([]Grid, len(grids))
	copy(shuffledGrids, grids)
	rng.Shuffle(len(shuffledGrids), func(i, j int) {
		shuffledGrids[i], shuffledGrids[j] = shuffledGrids[j], shuffledGrids[i]
	})

	for byteVal := 0; byteVal < 256; byteVal++ {
		targetGrid := shuffledGrids[byteVal]
		for _, positions := range hintPositions {
			var rawParts [4]hintPart
			for i, pos := range positions {
				val := targetGrid[pos] // 1..4
				rawParts[i] = hintPart{val: val, pos: pos}
			}
			if !hasUniqueMatch(grids, rawParts) {
				continue
			}

			var currentHints [4]byte
			for i, p := range rawParts {
				currentHints[i] = t.layout.encodeHint(p.val-1, p.pos)
			}
			t.EncodeTable[byteVal] = append(t.EncodeTable[byteVal], currentHints)
			key := packHintsToKey(currentHints)
			t.DecodeMap[key] = byte(byteVal)
		}
	}
	return t, nil
}

func packHintsToKey(hints [4]byte) uint32 {
	// Sorting network for 4 elements.
	if hints[0] > hints[1] {
		hints[0], hints[1] = hints[1], hints[0]
	}
	if hints[2] > hints[3] {
		hints[2], hints[3] = hints[3], hints[2]
	}
	if hints[0] > hints[2] {
		hints[0], hints[2] = hints[2], hints[0]
	}
	if hints[1] > hints[3] {
		hints[1], hints[3] = hints[3], hints[1]
	}
	if hints[1] > hints[2] {
		hints[1], hints[2] = hints[2], hints[1]
	}

	return uint32(hints[0])<<24 | uint32(hints[1])<<16 | uint32(hints[2])<<8 | uint32(hints[3])
}
