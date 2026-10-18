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
	"io"
	"slices"
	"sync"
)

// presizedUploadBytes bounds how much of a declared Content-Length is allocated before any byte arrives; it
// matches the largest batch a stream client sends.
const presizedUploadBytes = 256 << 10

// uploadScratch holds the buffers one upload request needs, so a busy session reuses them instead of
// allocating a body-sized slice per request.
type uploadScratch struct {
	body    []byte
	decoded []byte
}

var uploadScratchPool = sync.Pool{New: func() any { return new(uploadScratch) }}

func getUploadScratch() *uploadScratch { return uploadScratchPool.Get().(*uploadScratch) }

func putUploadScratch(sc *uploadScratch) {
	// Past the request cap the buffer only exists because of growth; do not keep it alive.
	if cap(sc.body) > maxUploadBytes+1 {
		sc.body = nil
	}
	if cap(sc.decoded) > maxUploadBytes {
		sc.decoded = nil
	}
	uploadScratchPool.Put(sc)
}

// readBody reads up to limit bytes of r into sc.body. length is the declared body size, or -1 when unknown.
func (sc *uploadScratch) readBody(r io.Reader, length int64, limit int) ([]byte, error) {
	if length >= 0 {
		// One spare byte lets the final Read report EOF without growing the buffer.
		if want := int(min(length, presizedUploadBytes)) + 1; cap(sc.body) < want {
			sc.body = make([]byte, 0, want)
		}
	}
	b, err := appendUpTo(sc.body[:0], r, limit)
	sc.body = b
	return b, err
}

// decodeLines base64-decodes every non-blank line of encoded and concatenates the results in sc.decoded.
func (sc *uploadScratch) decodeLines(encoded []byte) ([]byte, error) {
	sc.decoded = slices.Grow(sc.decoded[:0], base64.StdEncoding.DecodedLen(len(encoded)))
	out := sc.decoded
	for len(encoded) > 0 {
		var line []byte
		line, encoded, _ = bytes.Cut(encoded, []byte{'\n'})
		if line = bytes.TrimSpace(line); len(line) == 0 {
			continue
		}
		n, err := base64.StdEncoding.Decode(out[len(out):cap(out)], line)
		if err != nil {
			return nil, err
		}
		out = out[:len(out)+n]
	}
	return out, nil
}

// appendUpTo reads r into b until EOF or until b holds limit bytes, growing b as needed.
func appendUpTo(b []byte, r io.Reader, limit int) ([]byte, error) {
	for len(b) < limit {
		if len(b) == cap(b) {
			b = slices.Grow(b, 4096)
		}
		n, err := r.Read(b[len(b):min(cap(b), limit)])
		b = b[:len(b)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return b, err
		}
	}
	return b, nil
}
