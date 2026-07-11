package sudoku

import (
	"strings"

	sudokut "github.com/sagernet/sing-box/transport/sudoku"
	sudokuo "github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func resolveTableType(ascii string) string {
	if v := strings.TrimSpace(ascii); v != "" {
		return v
	}
	return "prefer_ascii"
}

func resolvePadding(defaultMin, defaultMax int, minOpt, maxOpt *int) (min, max int) {
	min, max = defaultMin, defaultMax
	if minOpt != nil {
		min = *minOpt
	}
	if maxOpt != nil {
		max = *maxOpt
	}
	if minOpt == nil && maxOpt != nil && max < min {
		min = max
	}
	if maxOpt == nil && minOpt != nil && max < min {
		max = min
	}
	return min, max
}

func resolveBool(defaultVal bool, opt *bool) bool {
	if opt != nil {
		return *opt
	}
	return defaultVal
}

func resolveHTTPMaskMode(defaultMode, optMode, strategy string) string {
	if optMode != "" {
		return optMode
	}
	if strings.EqualFold(strings.TrimSpace(strategy), "websocket") {
		return "ws"
	}
	return defaultMode
}

func resolveMultiplex(defaultMode, topLevel, legacy string) string {
	for _, mode := range []string{legacy, topLevel, defaultMode} {
		if mode = strings.TrimSpace(mode); mode != "" {
			return strings.ToLower(mode)
		}
	}
	return "off"
}

func buildProtocolTables(key, ascii, customTable string, customTables []string, clientSeed bool) ([]*sudokuo.Table, error) {
	if clientSeed {
		key = sudokut.ClientAEADSeed(key)
	}
	patterns := customTables
	if len(patterns) == 0 && strings.TrimSpace(customTable) != "" {
		patterns = []string{strings.TrimSpace(customTable)}
	}
	if !clientSeed && len(patterns) > 0 && strings.TrimSpace(patterns[0]) != "" {
		asciiMode, err := sudokuo.ParseASCIIMode(resolveTableType(ascii))
		if err != nil {
			return nil, err
		}
		if asciiMode.Uplink == "entropy" {
			patterns = append([]string{""}, patterns...)
		}
	}
	return sudokut.NewTablesWithCustomPatterns(key, resolveTableType(ascii), "", patterns)
}

func applyProtocolTables(cfg *sudokut.ProtocolConfig, tables []*sudokuo.Table) {
	if len(tables) == 1 {
		cfg.Table = tables[0]
		return
	}
	cfg.Tables = tables
}

func allowSessionMux(cfg *sudokut.ProtocolConfig) bool {
	return cfg != nil && cfg.SessionMuxEnabled()
}
