package crypto

import "sync"

// retainedFrameBytes is the largest frame a connection keeps between calls.
// Larger frames borrow a full-size buffer from framePool only while a Read or
// Write is using them, so an idle connection holds at most this much.
const retainedFrameBytes = 512

const pooledFrameBytes = 2 + maxFrameBodySize

var framePool = sync.Pool{New: func() any {
	b := make([]byte, pooledFrameBytes)
	return &b
}}

// growFrame returns buf resliced to n bytes, replacing it when too small. Frames
// above retainedFrameBytes come from framePool and must go back with releaseFrame.
func growFrame(buf []byte, n int) []byte {
	if cap(buf) >= n {
		return buf[:n]
	}
	if n <= retainedFrameBytes {
		return make([]byte, n)
	}
	return (*framePool.Get().(*[]byte))[:n]
}

// releaseFrame hands a pooled frame back and keeps a small one.
func releaseFrame(buf []byte) []byte {
	if cap(buf) <= retainedFrameBytes {
		return buf
	}
	buf = buf[:cap(buf)]
	framePool.Put(&buf)
	return nil
}
