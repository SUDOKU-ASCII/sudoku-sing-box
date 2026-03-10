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
package logx

import (
	"log"
	"strings"
	"sync"
)

var stdInstallOnce sync.Once

// InstallStd routes the standard library's global logger through logx formatting.
// It is safe to call multiple times.
func InstallStd() {
	stdInstallOnce.Do(func() {
		log.SetFlags(0)
		log.SetPrefix("")
		log.SetOutput(stdWriter{})
	})
}

type stdWriter struct{}

func (stdWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\r\n")
	component, rest := parseLeadingComponents(msg)
	if strings.TrimSpace(rest) == "" {
		rest = msg
	}
	logf(LevelInfo, component, "%s", rest)
	return len(p), nil
}

func parseLeadingComponents(s string) (string, string) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return "", s
	}

	components := make([]string, 0, 4)
	rest := s
	for strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end <= 1 {
			break
		}
		seg := strings.TrimSpace(rest[1:end])
		if seg == "" {
			break
		}
		components = append(components, seg)
		rest = strings.TrimSpace(rest[end+1:])
	}

	if len(components) == 0 {
		return "", s
	}
	return strings.Join(components, "/"), rest
}
