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
	"io"
	"math/rand/v2"
	"testing"
)

// choppedReader returns src in reads of random size, like a chunked body does.
type choppedReader struct {
	src []byte
	rng *rand.Rand
}

func (r *choppedReader) Read(p []byte) (int, error) {
	if len(r.src) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(r.src), 1+r.rng.IntN(pullPayloadBytes))
	copy(p, r.src[:n])
	r.src = r.src[n:]
	return n, nil
}

// TestStreamPull_PooledPayloadsStayIntact runs the pull reader against a conn that recycles drained payloads, with
// reads smaller than the payloads, and checks the byte stream survives buffer reuse.
func TestStreamPull_PooledPayloadsStayIntact(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	want := make([]byte, 8<<20)
	for i := range want {
		want[i] = byte(rng.Uint32())
	}

	c := newChanConn()
	go func() {
		_, err := readStreamPull(&choppedReader{src: want, rng: rng}, func(p []byte) bool {
			select {
			case c.rxc <- p:
				return true
			case <-c.closed:
				return false
			}
		})
		if err != nil {
			t.Errorf("readStreamPull: %v", err)
		}
		c.markReadEOF()
	}()

	var got bytes.Buffer
	readRng := rand.New(rand.NewPCG(3, 4))
	buf := make([]byte, 40*1024)
	for {
		n, err := c.Read(buf[:1+readRng.IntN(len(buf))])
		got.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("received %d bytes that differ from the %d sent", got.Len(), len(want))
	}
}
