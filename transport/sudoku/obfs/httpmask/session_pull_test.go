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

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http/httputil"
	"testing"
)

// chunkedReference frames pieces the way net/http does, which is what pull produced before it framed by hand.
func chunkedReference(pieces ...[]byte) []byte {
	var out bytes.Buffer
	cw := httputil.NewChunkedWriter(&out)
	for _, p := range pieces {
		_, _ = cw.Write(p)
	}
	return out.Bytes()
}

func TestAppendPullFrame_MatchesChunkedWriter(t *testing.T) {
	for _, size := range []int{1, 2, 3, 15, 16, 17, 4095, 4096, downlinkReadBytes} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i * 7)
		}
		line := []byte(base64.StdEncoding.EncodeToString(payload))

		for _, tc := range []struct {
			name  string
			codec sessionCodec
			want  []byte
		}{
			{"stream", streamCodec, chunkedReference(payload)},
			{"poll", pollCodec, chunkedReference(line, []byte("\n"))},
		} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, size), func(t *testing.T) {
				// A dirty prefix proves the frame is appended rather than overwritten.
				got := tc.codec.appendPullFrame([]byte("x"), payload)
				if !bytes.Equal(got[1:], tc.want) {
					t.Fatalf("frame differs from chunked writer output")
				}
				if len(got)-1 > downlinkFrameBytes {
					t.Fatalf("frame %d bytes exceeds downlinkFrameBytes %d", len(got)-1, downlinkFrameBytes)
				}
			})
		}
	}
}

func TestPullEnds_MatchChunkedWriterClose(t *testing.T) {
	var closed bytes.Buffer
	_ = httputil.NewChunkedWriter(&closed).Close()
	closed.WriteString("\r\n")
	if !bytes.Equal(pullEnd, closed.Bytes()) {
		t.Fatalf("pullEnd = %q, want %q", pullEnd, closed.Bytes())
	}
	if want := "0\r\nX-Sudoku-Stream-EOF: 1\r\n\r\n"; string(pullEndAfterEOF) != want {
		t.Fatalf("pullEndAfterEOF = %q, want %q", pullEndAfterEOF, want)
	}
	if want := string(chunkedReference([]byte("\n"))) + string(pullEnd); string(pollKeepaliveEnd) != want {
		t.Fatalf("pollKeepaliveEnd = %q, want %q", pollKeepaliveEnd, want)
	}
}
