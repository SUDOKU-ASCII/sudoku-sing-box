package crypto

import (
	"io"
	"net"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

// WriteTo forwards authenticated plaintext directly from the record buffer.
// The read lock keeps that storage alive during each destination Write. A short
// write leaves the unwritten suffix available to the next Read or WriteTo.
func (c *RecordConn) WriteTo(w io.Writer) (int64, error) {
	if c == nil || c.Conn == nil {
		return 0, net.ErrClosed
	}
	if c.method == "none" {
		return connutil.Copy(w, c.Conn)
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()

	var total int64
	for {
		if !c.hasPendingPlainLocked() {
			if _, err := c.readLocked(nil); err != nil {
				if err == io.EOF {
					err = nil
				}
				return total, err
			}
			if !c.hasPendingPlainLocked() {
				continue // An authenticated empty record carries no application data.
			}
		}
		p := c.readPlain[c.readOff:]
		n, err := w.Write(p)
		if n < 0 || n > len(p) {
			n, err = 0, io.ErrShortWrite
		}
		if n < len(p) && err == nil {
			err = io.ErrShortWrite
		}
		c.readOff += n
		total += int64(n)
		if !c.hasPendingPlainLocked() {
			c.readPlain = nil
			c.readOff = 0
		}
		if err != nil {
			return total, err
		}
	}
}
