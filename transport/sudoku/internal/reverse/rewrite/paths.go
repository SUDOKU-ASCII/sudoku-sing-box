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
package rewrite

import (
	"bytes"
	"strings"
)

func normalizedPrefixBytes(prefix string) []byte {
	p := NormalizePrefix(prefix)
	if p == "" {
		return nil
	}
	return []byte(p)
}

// NormalizePrefix returns prefix with a leading slash and no trailing one, or "" for the root.
func NormalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

// splicer builds a copy of src with some ranges replaced. It allocates only once the first
// edit arrives, so scanning input that needs no rewrite costs nothing.
type splicer struct {
	src    []byte
	out    []byte
	last   int // src[:last] has been consumed
	edited bool
}

// replace swaps src[start:end] for with; edits must come in ascending order.
func (s *splicer) replace(start, end int, with []byte) {
	if !s.edited {
		s.out = make([]byte, 0, len(s.src)+len(s.src)/16)
		s.edited = true
	}
	s.out = append(s.out, s.src[s.last:start]...)
	s.out = append(s.out, with...)
	s.last = end
}

// bytes returns src itself when nothing was replaced.
func (s *splicer) bytes() []byte {
	if !s.edited {
		return s.src
	}
	return append(s.out, s.src[s.last:]...)
}

func rewriteRootAbsolutePaths(in []byte, prefix string) []byte {
	p := normalizedPrefixBytes(prefix)
	if len(in) == 0 || p == nil {
		return in
	}
	s := splicer{src: in}
	s.prefixRootAbsolute(0, len(in), p)
	return s.bytes()
}

// prefixRootAbsolute inserts prefix before each root-absolute URL in src[from:to],
// reading that range as a document of its own.
func (s *splicer) prefixRootAbsolute(from, to int, prefix []byte) {
	in := s.src[from:to]
	insert := func(slash int) { s.replace(from+slash, from+slash, prefix) }

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
			if urlHasPathPrefix(in[slash:], prefix) {
				continue
			}
			insert(slash)
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
		if urlHasPathPrefix(in[slash:], prefix) {
			continue
		}
		insert(slash)
		i = slash
	}
}

func rewriteHTMLSrcset(in []byte, prefix string) []byte {
	p := normalizedPrefixBytes(prefix)
	if p == nil {
		return in
	}

	s := splicer{src: in}
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

		s.prefixSrcsetURLs(valStart, valEnd, p)
		i = valEnd
	}
	return s.bytes()
}

// prefixSrcsetURLs inserts prefix before each root-absolute candidate URL of the srcset
// value src[from:to].
func (s *splicer) prefixSrcsetURLs(from, to int, prefix []byte) {
	val := s.src[from:to]
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

		s.replace(from+i, from+i, prefix)
	}
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
