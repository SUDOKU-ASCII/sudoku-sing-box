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

func resolveConfigString(defaultVal, opt string) string {
	if opt != "" {
		return opt
	}
	return defaultVal
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

func buildProtocolTables(key, ascii, customTable string, customTables []string, clientSeed bool) ([]*sudokuo.Table, error) {
	if clientSeed {
		key = sudokut.ClientAEADSeed(key)
	}
	return sudokut.NewTablesWithCustomPatterns(key, resolveTableType(ascii), customTable, customTables)
}

func applyProtocolTables(cfg *sudokut.ProtocolConfig, tables []*sudokuo.Table) {
	if len(tables) == 1 {
		cfg.Table = tables[0]
		return
	}
	cfg.Tables = tables
}

func allowHTTPMaskMux(cfg *sudokut.ProtocolConfig) bool {
	if cfg == nil || cfg.DisableHTTPMask || !strings.EqualFold(strings.TrimSpace(cfg.HTTPMaskMultiplex), "on") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)) {
	case "stream", "poll", "auto", "ws":
		return true
	default:
		return false
	}
}
