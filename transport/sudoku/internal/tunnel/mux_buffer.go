package tunnel

import "sync"

// A chunk owns its complete backing slice until it has been consumed or dropped.
// Keeping the queue link in the pooled object avoids allocating queue entries.
type muxChunk struct {
	data []byte
	off  int
	next *muxChunk
	pool *sync.Pool
}

// Power-of-two classes keep small messages from retaining maximum-size frames.
var muxChunkPools [9]sync.Pool // 1 KiB through 256 KiB

func acquireMuxChunk(n int) *muxChunk {
	size, class := 1024, 0
	for size < n {
		size <<= 1
		class++
	}
	pool := &muxChunkPools[class]
	if cached := pool.Get(); cached != nil {
		chunk := cached.(*muxChunk)
		chunk.data = chunk.data[:n]
		return chunk
	}
	return &muxChunk{data: make([]byte, n, size), pool: pool}
}

func (chunk *muxChunk) release() {
	chunk.off = 0
	chunk.next = nil
	if chunk.pool != nil {
		chunk.pool.Put(chunk)
	}
}

// discardQueueLocked is used only when the local read side is explicitly closed.
// Remote/session errors still allow already accepted data to be drained.
func (c *muxStream) discardQueueLocked() {
	for c.head != nil {
		chunk := c.head
		c.head = chunk.next
		c.queuedBytes -= len(chunk.data) - chunk.off
		chunk.release()
	}
	c.tail = nil
}

// popChunkLocked transfers ownership to the caller. Unread bytes remain charged
// to the stream until consumed, so a blocked downstream cannot bypass the cap.
func (c *muxStream) popChunkLocked() *muxChunk {
	chunk := c.head
	c.head = chunk.next
	if c.head == nil {
		c.tail = nil
	}
	chunk.next = nil
	return chunk
}
