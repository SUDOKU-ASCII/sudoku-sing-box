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
package tunnel

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

var errProbeMismatch = errors.New("probe mismatch")

// probeAcceptingAfter asks every candidate for more bytes until minLen are available, then accepts
// only (want, wantMode). A candidate that is rejected earlier would leave a sole survivor, which
// the selector accepts without waiting for more bytes.
func probeAcceptingAfter(want *sudoku.Table, wantMode obfsUplinkMode, minLen int) HandshakeObfsProbeFunc {
	return func(probe []byte, table *sudoku.Table, mode obfsUplinkMode) error {
		if len(probe) < minLen {
			return io.ErrUnexpectedEOF
		}
		if table != want || mode != wantMode {
			return errProbeMismatch
		}
		return nil
	}
}

func TestSelectHandshakeObfsByProbe(t *testing.T) {
	tableA, tableB := &sudoku.Table{}, &sudoku.Table{}
	tables := []*sudoku.Table{tableA, tableB}
	data := bytes.Repeat([]byte{0xA5}, 300)

	cases := []struct {
		name    string
		reader  io.Reader
		minLen  int
		wantErr bool
	}{
		{name: "decided by buffered bytes", reader: bytes.NewReader(data), minLen: 100},
		{name: "needs more reads", reader: iotest.OneByteReader(bytes.NewReader(data)), minLen: 100},
		{name: "stream ends before enough bytes", reader: bytes.NewReader(data[:50]), minLen: 100, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br := bufio.NewReader(tc.reader)
			_, _ = br.Peek(1)

			sel, probed, err := SelectHandshakeObfsByProbe(br, tables, probeAcceptingAfter(tableB, obfsUplinkPacked, tc.minLen))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("select failed: %v", err)
			}
			if sel.Table != tableB || sel.UplinkMode != obfsUplinkPacked {
				t.Fatalf("selected wrong candidate: %+v", sel)
			}
			if len(probed) < tc.minLen || !bytes.HasPrefix(data, probed) {
				t.Fatalf("probe bytes are not a prefix of the stream (len=%d)", len(probed))
			}
		})
	}
}

func TestSelectHandshakeObfsByProbeRejectsEmptyCandidates(t *testing.T) {
	br := bufio.NewReader(bytes.NewReader([]byte{1}))
	if _, _, err := SelectHandshakeObfsByProbe(br, []*sudoku.Table{nil}, probeAcceptingAfter(nil, obfsUplinkPure, 0)); err == nil {
		t.Fatal("expected an error without candidates")
	}
}

// The common handshake is decided from bytes bufio already holds, without another read.
func BenchmarkSelectHandshakeObfsByProbe(b *testing.B) {
	table := &sudoku.Table{}
	tables := []*sudoku.Table{table}
	data := bytes.Repeat([]byte{0xA5}, 256)
	probe := probeAcceptingAfter(table, obfsUplinkPacked, len(data))

	src := bytes.NewReader(data)
	br := bufio.NewReader(src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src.Reset(data)
		br.Reset(src)
		_, _ = br.Peek(1)
		if _, _, err := SelectHandshakeObfsByProbe(br, tables, probe); err != nil {
			b.Fatal(err)
		}
	}
}
