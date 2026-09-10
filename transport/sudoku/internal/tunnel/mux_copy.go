package tunnel

import "io"

// WriteTo lets io.Copy forward received blocks without a relay-buffer copy.
// A detached block stays owned by this call while the destination is writing;
// closing the stream can release queued blocks but cannot recycle that block.
func (c *muxStream) WriteTo(w io.Writer) (int64, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	var total int64
	for {
		c.mu.Lock()
		err := c.waitReadableLocked()
		if err != nil {
			c.mu.Unlock()
			if err == io.EOF {
				err = nil
			}
			return total, err
		}
		chunk := c.popChunkLocked()
		c.mu.Unlock()

		p := chunk.data[chunk.off:]
		n, err := w.Write(p)
		if n < 0 || n > len(p) {
			n, err = 0, io.ErrShortWrite
		}
		if n < len(p) && err == nil {
			err = io.ErrShortWrite
		}
		total += int64(n)

		c.mu.Lock()
		chunk.off += n
		c.queuedBytes -= n
		if chunk.off == len(chunk.data) || c.localReadClosed {
			c.queuedBytes -= len(chunk.data) - chunk.off
			chunk.release()
		} else {
			// Preserve the unwritten suffix ahead of data received meanwhile.
			chunk.next = c.head
			c.head = chunk
			if c.tail == nil {
				c.tail = chunk
			}
		}
		c.mu.Unlock()
		if err != nil {
			return total, err
		}
	}
}
