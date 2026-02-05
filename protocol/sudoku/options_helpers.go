package sudoku

import "strings"

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
