package sudoku

import (
	"strings"

	sudokuc "github.com/sagernet/sing-box/transport/sudoku/crypto"
	obfssudoku "github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func ClientAEADSeed(key string) string {
	return clientTableSeed(key)
}

func clientTableSeed(key string) string {
	if recovered, err := sudokuc.RecoverPublicKey(strings.TrimSpace(key)); err == nil {
		return sudokuc.EncodePoint(recovered)
	}
	return strings.TrimSpace(key)
}

func NewTablesWithCustomPatterns(key string, tableType string, customTable string, customTables []string) ([]*obfssudoku.Table, error) {
	patterns := customTables
	if len(patterns) == 0 && strings.TrimSpace(customTable) != "" {
		patterns = []string{strings.TrimSpace(customTable)}
	}
	if len(patterns) == 0 {
		patterns = []string{""}
	}
	tableSet, err := obfssudoku.NewTableSet(strings.TrimSpace(key), strings.TrimSpace(tableType), patterns)
	if err != nil {
		return nil, err
	}
	return tableSet.Candidates(), nil
}

func GenKeyPair() (privateKey, publicKey string, err error) {
	keyPair, err := sudokuc.GenerateMasterKey()
	if err != nil {
		return "", "", err
	}
	privateKey, err = sudokuc.SplitPrivateKey(keyPair.Private)
	if err != nil {
		return "", "", err
	}
	return privateKey, sudokuc.EncodePoint(keyPair.Public), nil
}
