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
	"os"
	"testing"
)

// discardStdout points os.Stdout at /dev/null for the duration of the test or benchmark.
func discardStdout(tb testing.TB) {
	tb.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		tb.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = f
	tb.Cleanup(func() {
		os.Stdout = orig
		f.Close()
	})
}

func BenchmarkLogf(b *testing.B) {
	initOnce.Do(initConfig)
	discardStdout(b)
	b.Run("emitted", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			Infof("Server/UoT", "[User:%s] session start %d", "alice", i)
		}
	})
	b.Run("filtered", func(b *testing.B) {
		saved := minLevel
		minLevel = LevelError
		defer func() { minLevel = saved }()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			Infof("Server/UoT", "[User:%s] session start %d", "alice", i)
		}
	})
}
