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
package httpmask

import "strings"

// normalizePathRoot normalizes the configured path root into "/<segment>" form.
//
// It is intentionally strict: only a single path segment is allowed, consisting of
// [A-Za-z0-9_-]. Invalid inputs are treated as empty (disabled).
func normalizePathRoot(root string) string {
	root = strings.TrimSpace(root)
	root = strings.Trim(root, "/")
	if root == "" {
		return ""
	}
	for i := 0; i < len(root); i++ {
		c := root[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return ""
		}
	}
	return "/" + root
}

func joinPathRoot(root, path string) string {
	root = normalizePathRoot(root)
	if root == "" {
		return path
	}
	if path == "" {
		return root
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return root + path
}

func stripPathRoot(root, fullPath string) (string, bool) {
	root = normalizePathRoot(root)
	if root == "" {
		return fullPath, true
	}
	if !strings.HasPrefix(fullPath, root+"/") {
		return "", false
	}
	return strings.TrimPrefix(fullPath, root), true
}
