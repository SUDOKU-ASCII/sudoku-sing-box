/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package reverse

import (
	"bytes"
	"strings"
)

func normalizedPrefixBytes(prefix string) []byte {
	p := normalizePrefix(prefix)
	if p == "" {
		return nil
	}
	return []byte(p)
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" || prefix == "/" {
		return ""
	}
	return prefix
}

func rewriteRootAbsolutePaths(in []byte, prefix string) []byte {
	p := normalizedPrefixBytes(prefix)
	if len(in) == 0 || p == nil {
		return in
	}

	var (
		out  bytes.Buffer
		last int
	)
	out.Grow(len(in) + len(in)/16)

	writePrefixAt := func(slashIndex int) {
		out.Write(in[last:slashIndex])
		out.Write(p)
		last = slashIndex
	}

	for i := 0; i+1 < len(in); i++ {
		switch in[i] {
		case '"', '\'', '`':
			if i > 0 && in[i-1] == '\\' {
				continue
			}
			slash := i + 1
			if in[slash] != '/' {
				continue
			}
			if slash+1 < len(in) && in[slash+1] == '/' {
				continue
			}
			if slash+1 < len(in) && isURLTerminalByte(in[slash+1]) {
				continue
			}
			if urlHasPathPrefix(in[slash:], p) {
				continue
			}
			writePrefixAt(slash)
			i = slash
		default:
		}

		// CSS url(/...) or url("/...") is handled here for the unquoted variant.
		// Quoted url("/...") is already handled by the quote case above.
		if lowerASCII(in[i]) != 'u' || i+3 >= len(in) {
			continue
		}
		if lowerASCII(in[i+1]) != 'r' || lowerASCII(in[i+2]) != 'l' {
			continue
		}

		j := i + 3
		for j < len(in) && isSpace(in[j]) {
			j++
		}
		if j >= len(in) || in[j] != '(' {
			continue
		}
		j++
		for j < len(in) && isSpace(in[j]) {
			j++
		}
		if j >= len(in) || in[j] != '/' {
			continue
		}
		slash := j
		if slash+1 < len(in) && in[slash+1] == '/' {
			continue
		}
		if slash+1 < len(in) && isURLTerminalByte(in[slash+1]) {
			continue
		}
		if urlHasPathPrefix(in[slash:], p) {
			continue
		}
		writePrefixAt(slash)
		i = slash
	}

	if last == 0 {
		return in
	}
	out.Write(in[last:])
	return out.Bytes()
}

func rewriteHTMLSrcset(in []byte, prefix string) []byte {
	p := normalizedPrefixBytes(prefix)
	if p == nil {
		return in
	}

	var (
		out      bytes.Buffer
		last     int
		modified bool
	)
	out.Grow(len(in) + len(in)/32)

	for i := 0; i < len(in); i++ {
		if lowerASCII(in[i]) != 's' || i+6 >= len(in) {
			continue
		}
		if lowerASCII(in[i+1]) != 'r' || lowerASCII(in[i+2]) != 'c' || lowerASCII(in[i+3]) != 's' ||
			lowerASCII(in[i+4]) != 'e' || lowerASCII(in[i+5]) != 't' {
			continue
		}

		j := i + 6
		for j < len(in) && isSpace(in[j]) {
			j++
		}
		if j >= len(in) || in[j] != '=' {
			continue
		}
		j++
		for j < len(in) && isSpace(in[j]) {
			j++
		}
		if j >= len(in) {
			break
		}

		quote := in[j]
		if quote != '"' && quote != '\'' {
			continue
		}
		valStart := j + 1
		valEnd := valStart
		for valEnd < len(in) && in[valEnd] != quote {
			valEnd++
		}
		if valEnd >= len(in) {
			break
		}

		val := in[valStart:valEnd]
		newVal := rewriteSrcsetValue(val, p)
		if bytes.Equal(val, newVal) {
			i = valEnd
			continue
		}

		out.Write(in[last:valStart])
		out.Write(newVal)
		last = valEnd
		i = valEnd
		modified = true
	}

	if !modified {
		return in
	}
	out.Write(in[last:])
	return out.Bytes()
}

func rewriteSrcsetValue(val []byte, prefix []byte) []byte {
	if len(val) == 0 || len(prefix) == 0 {
		return val
	}
	var (
		out  bytes.Buffer
		last int
	)
	out.Grow(len(val) + len(val)/16)

	for i := 0; i < len(val); i++ {
		if val[i] != '/' {
			continue
		}
		if i+1 < len(val) && val[i+1] == '/' {
			continue
		}

		start := i == 0
		if !start {
			k := i - 1
			for k >= 0 && isSpace(val[k]) {
				k--
			}
			start = k < 0 || val[k] == ','
		}
		if !start {
			continue
		}
		if urlHasPathPrefix(val[i:], prefix) {
			continue
		}

		out.Write(val[last:i])
		out.Write(prefix)
		last = i
	}

	if last == 0 {
		return val
	}
	out.Write(val[last:])
	return out.Bytes()
}

func isURLTerminalByte(b byte) bool {
	switch b {
	case '"', '\'', '`', ')', ',', ';', ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}

func urlHasPathPrefix(u []byte, prefix []byte) bool {
	if len(u) == 0 || len(prefix) == 0 {
		return false
	}
	if !bytes.HasPrefix(u, prefix) {
		return false
	}
	if len(u) == len(prefix) {
		return true
	}
	switch u[len(prefix)] {
	case '/', '?', '#':
		return true
	default:
		return isURLTerminalByte(u[len(prefix)])
	}
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
