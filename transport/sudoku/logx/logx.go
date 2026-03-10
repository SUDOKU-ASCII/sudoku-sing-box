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
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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

func Debugf(component, format string, args ...any) { logf(LevelDebug, component, format, args...) }
func Infof(component, format string, args ...any)  { logf(LevelInfo, component, format, args...) }
func Warnf(component, format string, args ...any)  { logf(LevelWarn, component, format, args...) }
func Errorf(component, format string, args ...any) { logf(LevelError, component, format, args...) }

// Promptf prints a log-styled prompt without a trailing newline.
// It is intended for interactive CLI input.
func Promptf(component, format string, args ...any) {
	initOnce.Do(initConfig)
	ts := time.Now().Format("15:04:05")

	line := fmt.Sprintf(format, args...)
	if strings.TrimSpace(component) != "" {
		component = strings.TrimSpace(component)
		component = "[" + component + "]"
	}

	prefix := fmt.Sprintf("%s %s %s", Dim(ts), colorLevel(LevelInfo, strings.ToLower(levelName(LevelInfo))), Magenta(component))
	writeMu.Lock()
	_, _ = fmt.Fprint(os.Stdout, strings.TrimSpace(prefix), " ", line)
	writeMu.Unlock()
}

func Fatalf(component, format string, args ...any) {
	Errorf(component, format, args...)
	os.Exit(1)
}

func Panicf(component, format string, args ...any) {
	Errorf(component, format, args...)
	panic(fmt.Sprintf(format, args...))
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

	ts := time.Now().Format("15:04:05")
	levelText := strings.ToLower(levelName(lvl))

	line := fmt.Sprintf(format, args...)
	if strings.TrimSpace(component) != "" {
		component = strings.TrimSpace(component)
		component = "[" + component + "]"
	}

	prefix := fmt.Sprintf("%s %s %s", Dim(ts), colorLevel(lvl, levelText), Magenta(component))
	writeMu.Lock()
	_, _ = fmt.Fprintln(os.Stdout, strings.TrimSpace(prefix), line)
	writeMu.Unlock()
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

func levelName(lvl Level) string {
	switch lvl {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func colorLevel(lvl Level, s string) string {
	switch lvl {
	case LevelDebug:
		return Dim(s)
	case LevelInfo:
		return Cyan(s)
	case LevelWarn:
		return Yellow(s)
	case LevelError:
		return Red(s)
	default:
		return s
	}
}

func color(code, s string) string {
	if !useColor || s == "" {
		return s
	}
	return code + s + "\x1b[0m"
}
