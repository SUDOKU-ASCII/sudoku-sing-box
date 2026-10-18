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
package connutil

import (
	"io"
	"net"
	"sync"
)

// PipeBufferSize stays just below the AEAD record plaintext ceiling, reducing
// record count for large streams without forcing an extra tiny trailing record.
const PipeBufferSize = 63 * 1024

var pipeBufPool = sync.Pool{
	New: func() any {
		return new([PipeBufferSize]byte)
	},
}

// PipeConn copies data bidirectionally between a and b, then closes both.
//
// It attempts half-close when supported:
//   - after copying src->dst: CloseWrite(dst) and CloseRead(src)
func PipeConn(a, b net.Conn) {
	if a == nil || b == nil {
		if a != nil {
			_ = a.Close()
		}
		if b != nil {
			_ = b.Close()
		}
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = Copy(a, b)
		_ = TryCloseWrite(a)
		_ = TryCloseRead(b)
	}()

	go func() {
		defer wg.Done()
		_, _ = Copy(b, a)
		_ = TryCloseWrite(b)
		_ = TryCloseRead(a)
	}()

	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// Copy preserves io.Copy's optional transfer methods and borrows a relay buffer
// only when neither endpoint provides one. Wrappers can use it without hiding
// their underlying connection's optimized path.
func Copy(dst io.Writer, src io.Reader) (int64, error) {
	// Avoid borrowing a relay buffer when io.Copy can transfer directly.
	if writer, ok := src.(io.WriterTo); ok {
		return writer.WriteTo(dst)
	}
	if reader, ok := dst.(io.ReaderFrom); ok {
		return reader.ReadFrom(src)
	}
	return copyAdaptive(dst, src)
}

// smallRelayBuffer is what a relay reads into while its source may stay silent.
const smallRelayBuffer = 2 * 1024

var smallRelayPool = sync.Pool{
	New: func() any {
		return new([smallRelayBuffer]byte)
	},
}

// copyAdaptive relays with a small buffer and moves to a PipeBufferSize one
// while reads keep filling it, so an idle relay holds a couple of KiB instead
// of a 63 KiB buffer parked inside a blocked Read. The big buffer goes back as
// soon as a read comes up short.
func copyAdaptive(dst io.Writer, src io.Reader) (written int64, err error) {
	small := smallRelayPool.Get().(*[smallRelayBuffer]byte)
	defer smallRelayPool.Put(small)
	var big *[PipeBufferSize]byte
	defer func() {
		if big != nil {
			pipeBufPool.Put(big)
		}
	}()

	buf := small[:]
	for {
		nr, rerr := src.Read(buf)
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			written += int64(max(nw, 0))
			if werr == nil && nw != nr {
				werr = io.ErrShortWrite
			}
			if werr != nil {
				return written, werr
			}
			switch {
			case big == nil && nr == len(buf):
				big = pipeBufPool.Get().(*[PipeBufferSize]byte)
				buf = big[:]
			case big != nil && nr < smallRelayBuffer:
				pipeBufPool.Put(big)
				big, buf = nil, small[:]
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				rerr = nil
			}
			return written, rerr
		}
	}
}
