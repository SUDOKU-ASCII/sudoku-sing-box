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

// rewriteHTMLRootAbsolutePaths rewrites root-absolute URLs in HTML while keeping inline scripts intact.
func rewriteHTMLRootAbsolutePaths(in []byte, prefix string) []byte {
	p := normalizedPrefixBytes(prefix)
	if len(in) == 0 || p == nil {
		return in
	}

	open := indexHTMLScriptOpen(in, 0)
	if open < 0 {
		return rewriteRootAbsolutePaths(in, prefix)
	}

	// Script bodies are copied untouched; only the markup around them is rewritten.
	s := splicer{src: in}
	last := 0
	for open >= 0 {
		tagEnd := findHTMLTagEnd(in, open)
		if tagEnd < 0 {
			return rewriteRootAbsolutePaths(in, prefix)
		}

		contentStart := tagEnd + 1
		close := indexHTMLScriptClose(in, contentStart)
		if close < 0 {
			return rewriteRootAbsolutePaths(in, prefix)
		}

		s.prefixRootAbsolute(last, contentStart, p)
		last = close
		open = indexHTMLScriptOpen(in, last)
	}
	s.prefixRootAbsolute(last, len(in), p)
	return s.bytes()
}

func indexHTMLScriptOpen(in []byte, from int) int {
	const needle = "script"
	for i := from; i+1+len(needle) <= len(in); i++ {
		if in[i] != '<' {
			continue
		}
		if i+1 < len(in) && in[i+1] == '/' {
			continue
		}
		j := i + 1
		if lowerASCII(in[j]) != 's' || lowerASCII(in[j+1]) != 'c' || lowerASCII(in[j+2]) != 'r' ||
			lowerASCII(in[j+3]) != 'i' || lowerASCII(in[j+4]) != 'p' || lowerASCII(in[j+5]) != 't' {
			continue
		}
		end := j + len(needle)
		if end >= len(in) {
			return i
		}
		switch in[end] {
		case '>', '/':
			return i
		default:
			if isSpace(in[end]) {
				return i
			}
		}
	}
	return -1
}

func indexHTMLScriptClose(in []byte, from int) int {
	const needle = "script"
	for i := from; i+2+len(needle) <= len(in); i++ {
		if in[i] != '<' || in[i+1] != '/' {
			continue
		}
		j := i + 2
		if lowerASCII(in[j]) != 's' || lowerASCII(in[j+1]) != 'c' || lowerASCII(in[j+2]) != 'r' ||
			lowerASCII(in[j+3]) != 'i' || lowerASCII(in[j+4]) != 'p' || lowerASCII(in[j+5]) != 't' {
			continue
		}
		end := j + len(needle)
		if end >= len(in) {
			return i
		}
		switch in[end] {
		case '>', '/':
			return i
		default:
			if isSpace(in[end]) {
				return i
			}
		}
	}
	return -1
}

func findHTMLTagEnd(in []byte, start int) int {
	var quote byte
	for i := start; i < len(in); i++ {
		c := in[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i
		default:
		}
	}
	return -1
}
