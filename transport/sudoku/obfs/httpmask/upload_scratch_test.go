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
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSessionCodec_ReadUpload(t *testing.T) {
	line := func(raw string) string { return base64.StdEncoding.EncodeToString([]byte(raw)) }
	big := strings.Repeat("0123456789abcdef", 1024) // 16KiB: larger than the initial scratch capacity

	for _, tc := range []struct {
		name    string
		codec   sessionCodec
		body    string
		length  int64
		want    string
		wantErr error // nil: success; errAny: any error
	}{
		{name: "stream known length", codec: streamCodec, body: "hello", length: 5, want: "hello"},
		{name: "stream unknown length grows", codec: streamCodec, body: big, length: -1, want: big},
		{name: "stream empty", codec: streamCodec, body: "", length: 0, want: ""},
		{name: "stream at the cap", codec: streamCodec, body: strings.Repeat("x", maxUploadBytes), length: maxUploadBytes, want: strings.Repeat("x", maxUploadBytes)},
		{name: "stream over the cap", codec: streamCodec, body: strings.Repeat("x", maxUploadBytes+1), length: -1, wantErr: errUploadTooLarge},
		{name: "poll joins lines and skips blanks", codec: pollCodec, body: line("ab") + "\n\n  " + line("cde") + " \r\n" + line(big) + "\n", length: -1, want: "abcde" + big},
		{name: "poll empty", codec: pollCodec, body: "\n", length: 1, want: ""},
		{name: "poll bad base64", codec: pollCodec, body: line("ok") + "\n!!!!\n", length: -1, wantErr: errAny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := getUploadScratch()
			defer putUploadScratch(sc)
			// Run twice so the second pass exercises a reused (dirty) scratch buffer.
			for pass := 0; pass < 2; pass++ {
				got, err := tc.codec.readUpload(sc, strings.NewReader(tc.body), tc.length)
				switch {
				case tc.wantErr == nil && err != nil:
					t.Fatalf("pass %d: %v", pass, err)
				case tc.wantErr == errAny && err == nil, tc.wantErr != nil && tc.wantErr != errAny && !errors.Is(err, tc.wantErr):
					t.Fatalf("pass %d: err = %v, want %v", pass, err, tc.wantErr)
				case tc.wantErr == nil && !bytes.Equal(got, []byte(tc.want)):
					t.Fatalf("pass %d: payload = %d bytes, want %d", pass, len(got), len(tc.want))
				}
			}
		})
	}
}

var errAny = errors.New("any error")

func TestAppendUpTo_StopsAtLimit(t *testing.T) {
	got, err := appendUpTo(nil, strings.NewReader("0123456789"), 4)
	if err != nil || string(got) != "0123" {
		t.Fatalf("got %q, %v", got, err)
	}
	failing := io.MultiReader(strings.NewReader("ab"), errReader{})
	got, err = appendUpTo(nil, failing, 100)
	if err == nil || string(got) != "ab" {
		t.Fatalf("got %q, %v; want the bytes read before the error", got, err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }
