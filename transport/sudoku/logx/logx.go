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
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/term"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	initOnce sync.Once
	minLevel Level = LevelInfo
	useColor bool

	writeMu sync.Mutex
)

const maxPooledBufBytes = 4 << 10

var bufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 256)
	return &b
}}

var (
	levelNames  = [...]string{LevelDebug: "debug", LevelInfo: "info", LevelWarn: "warn", LevelError: "error"}
	levelColors = [...]string{LevelDebug: "\x1b[2m", LevelInfo: "\x1b[36m", LevelWarn: "\x1b[33m", LevelError: "\x1b[31m"}
)

func Infof(component, format string, args ...any)  { logf(LevelInfo, component, format, args...) }
func Warnf(component, format string, args ...any)  { logf(LevelWarn, component, format, args...) }
func Errorf(component, format string, args ...any) { logf(LevelError, component, format, args...) }

// Promptf prints a log-styled prompt without a trailing newline.
// It is intended for interactive CLI input.
func Promptf(component, format string, args ...any) {
	initOnce.Do(initConfig)
	emit(LevelInfo, component, format, args, false)
}

func Fatalf(component, format string, args ...any) {
	Errorf(component, format, args...)
	os.Exit(1)
}

func Dim(s string) string     { return color("\x1b[2m", s) }
func Cyan(s string) string    { return color("\x1b[36m", s) }
func Yellow(s string) string  { return color("\x1b[33m", s) }
func Green(s string) string   { return color("\x1b[32m", s) }
func Magenta(s string) string { return color("\x1b[35m", s) }
func Red(s string) string     { return color("\x1b[31m", s) }
func Bold(s string) string    { return color("\x1b[1m", s) }

func logf(lvl Level, component, format string, args ...any) {
	initOnce.Do(initConfig)
	if lvl < minLevel {
		return
	}
	emit(lvl, component, format, args, true)
}

// emit writes "<time> <level> [component] <message>" with a single write, reusing pooled buffers.
func emit(lvl Level, component, format string, args []any, newline bool) {
	bp := bufPool.Get().(*[]byte)
	buf := (*bp)[:0]

	var clock [8]byte
	buf = appendStyled(buf, "\x1b[2m", time.Now().AppendFormat(clock[:0], "15:04:05"))
	buf = append(buf, ' ')
	buf = appendStyled(buf, levelColors[lvl], levelNames[lvl])
	buf = append(buf, ' ')
	buf = appendComponent(buf, component)
	buf = bytes.TrimRightFunc(buf, unicode.IsSpace)
	buf = append(buf, ' ')
	buf = fmt.Appendf(buf, format, args...)
	if newline {
		buf = append(buf, '\n')
	}

	writeMu.Lock()
	_, _ = os.Stdout.Write(buf)
	writeMu.Unlock()

	if cap(buf) <= maxPooledBufBytes {
		*bp = buf
		bufPool.Put(bp)
	}
}

func initConfig() {
	useColor = detectColor()
	minLevel = parseLevel(os.Getenv("SUDOKU_LOG_LEVEL"))
}

func parseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "dbg":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error", "err":
		return LevelError
	case "info", "":
		return LevelInfo
	default:
		return LevelInfo
	}
}

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	fd := int(os.Stdout.Fd())
	if fd <= 0 || !term.IsTerminal(fd) {
		return false
	}
	return true
}

func color(code, s string) string {
	if !useColor || s == "" {
		return s
	}
	return code + s + "\x1b[0m"
}

// appendStyled appends s wrapped in an ANSI color code when color output is on.
func appendStyled[S ~string | ~[]byte](buf []byte, code string, s S) []byte {
	if !useColor || len(s) == 0 {
		return append(buf, s...)
	}
	buf = append(buf, code...)
	buf = append(buf, s...)
	return append(buf, "\x1b[0m"...)
}

// appendComponent appends "[component]" in magenta; a blank component is passed through as is.
func appendComponent(buf []byte, component string) []byte {
	trimmed := strings.TrimSpace(component)
	if trimmed == "" {
		return appendStyled(buf, "\x1b[35m", component)
	}
	if useColor {
		buf = append(buf, "\x1b[35m"...)
	}
	buf = append(buf, '[')
	buf = append(buf, trimmed...)
	buf = append(buf, ']')
	if useColor {
		buf = append(buf, "\x1b[0m"...)
	}
	return buf
}
