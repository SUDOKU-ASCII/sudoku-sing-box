package sudoku

import (
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"
)

func mustTable(tb testing.TB, key, mode string) *Table {
	tb.Helper()
	table, err := NewTableWithCustom(key, mode, "")
	if err != nil {
		tb.Fatal(err)
	}
	return table
}

func randomBytes(tb testing.TB, n int) []byte {
	tb.Helper()
	p := make([]byte, n)
	if _, err := rand.Read(p); err != nil {
		tb.Fatal(err)
	}
	return p
}

// discardConn swallows writes and reads as an empty stream; the other conn
// stubs embed it and override what they need.
type discardConn struct{}

func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)      { return len(p), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return nil }
func (discardConn) RemoteAddr() net.Addr             { return nil }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

// memConn reads from readBuf once and appends every write to writeBuf.
type memConn struct {
	discardConn
	readBuf  []byte
	writeBuf []byte
}

func (m *memConn) Read(b []byte) (int, error) {
	if len(m.readBuf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, m.readBuf)
	m.readBuf = m.readBuf[n:]
	return n, nil
}

func (m *memConn) Write(b []byte) (int, error) {
	m.writeBuf = append(m.writeBuf, b...)
	return len(b), nil
}

// writeOnlyConn forwards writes to Writer and reads as empty.
type writeOnlyConn struct {
	discardConn
	io.Writer
}

func (c writeOnlyConn) Write(p []byte) (int, error) { return c.Writer.Write(p) }

// cyclicReadConn replays data forever and rejects writes.
type cyclicReadConn struct {
	discardConn
	data []byte
	off  int
}

func (c *cyclicReadConn) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		copied := copy(p[n:], c.data[c.off:])
		n += copied
		c.off += copied
		if c.off == len(c.data) {
			c.off = 0
		}
	}
	return n, nil
}

func (*cyclicReadConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// isHint reports whether b is a hint-class byte under the layout.
func isHint(l *byteLayout, b byte) bool { return l.hintIdx[b] != notHint }

// puzzlesOf returns the puzzles of plaintext byte b as four hint bytes each.
func puzzlesOf(tb *Table, b byte) [][4]byte {
	packed := tb.puzzles[tb.puzzleStart[b]:tb.puzzleStart[int(b)+1]]
	out := make([][4]byte, len(packed))
	for i, p := range packed {
		out[i] = [4]byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)}
	}
	return out
}
