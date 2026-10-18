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
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const (
	downlinkReadBytes = 32 * 1024
	// downlinkFrameBytes fits the largest frame: a poll payload as base64 plus its chunk framing and newline chunk.
	downlinkFrameBytes = 8 + (downlinkReadBytes+2)/3*4 + len(pollNewlineChunk)

	// pollNewlineChunk is the one-byte chunk that ends every poll line and doubles as the idle keepalive.
	pollNewlineChunk = "1\r\n\n\r\n"
)

// Bodies end with the zero chunk; EOF is reported as a trailer, which is why the response header announces one.
var (
	pullEnd          = []byte("0\r\n\r\n")
	pullEndAfterEOF  = []byte("0\r\n" + tunnelStreamEOFHeader + ": 1\r\n\r\n")
	pollKeepaliveEnd = []byte(pollNewlineChunk + "0\r\n\r\n")
)

// downlinkScratch holds the buffers a downlink payload needs between the tunnel read and the socket write; a pull
// takes one only while it has bytes to move, so an idle pull holds none.
type downlinkScratch struct {
	read  [downlinkReadBytes]byte
	frame []byte
}

var downlinkScratchPool = sync.Pool{New: func() any {
	return &downlinkScratch{frame: make([]byte, 0, downlinkFrameBytes)}
}}

func getDownlinkScratch() *downlinkScratch { return downlinkScratchPool.Get().(*downlinkScratch) }

func putDownlinkScratch(sc *downlinkScratch) {
	if sc != nil {
		downlinkScratchPool.Put(sc)
	}
}

// pull answers GET <root>/stream with a chunked downlink until the session has
// nothing more to say, the response breaks or another pull takes over.
func (s *TunnelServer) pull(rawConn net.Conn, token string, codec sessionCodec) (HandleResult, net.Conn, error) {
	sess, ok := s.sessions.get(token)
	if !ok {
		return replyDone(rawConn, http.StatusForbidden, "forbidden")
	}

	lease, ok := s.sessions.beginPull(token, sess, rawConn)
	if !ok {
		return replyDone(rawConn, http.StatusForbidden, "forbidden")
	}
	defer sess.endPull(lease)

	if err := writeSessionPullResponseHeader(rawConn); err != nil {
		_ = rawConn.Close()
		return HandleDone, nil, err
	}

	// nil once a frame failed to go out whole: the body is already broken, so no terminator.
	end := pullEnd
	defer func() {
		if end != nil {
			_, _ = rawConn.Write(end)
		}
		_ = rawConn.Close()
	}()

	for {
		select {
		case <-lease.cancel:
			return HandleDone, nil, nil
		default:
		}

		payload := sess.pendingDownlink
		var scratch *downlinkScratch
		var readErr error
		if len(payload) == 0 {
			_ = sess.conn.SetReadDeadline(time.Now().Add(s.pullReadTimeout))
			if readErr = sess.conn.awaitReadable(); readErr == nil {
				scratch = getDownlinkScratch()
				var n int
				n, readErr = sess.conn.Read(scratch.read[:])
				payload = scratch.read[:n]
			}
		}
		if len(payload) > 0 {
			if scratch == nil {
				scratch = getDownlinkScratch()
			}
			if !s.sessions.touch(token, sess) {
				putDownlinkScratch(scratch)
				return HandleDone, nil, nil
			}
			scratch.frame = codec.appendPullFrame(scratch.frame[:0], payload)
			_ = rawConn.SetWriteDeadline(time.Now().Add(s.pullReadTimeout))
			if err := connutil.WriteFull(rawConn, scratch.frame); err != nil {
				// The HTTP response may be reset by a CDN while the backing
				// tunnel is still healthy. The client will establish a fresh
				// pull request, so never destroy the session for this request's
				// downstream socket failure; its next pull resends the payload.
				sess.pendingDownlink = append(sess.pendingDownlink[:0], payload...)
				putDownlinkScratch(scratch)
				end = nil
				return HandleDone, nil, nil
			}
			_ = rawConn.SetWriteDeadline(time.Time{})
			sess.pendingDownlink = nil
		}
		putDownlinkScratch(scratch)
		if readErr == nil {
			continue
		}

		switch {
		case errors.Is(readErr, os.ErrDeadlineExceeded):
			select {
			case <-lease.cancel:
			default:
				if codec.poll {
					end = pollKeepaliveEnd
					s.sessions.touch(token, sess)
				}
			}
		case errors.Is(readErr, io.EOF):
			end = pullEndAfterEOF
			s.sessions.closeDownlink(token)
		default:
			s.sessions.close(token)
		}
		return HandleDone, nil, nil
	}
}

// appendPullFrame appends the chunked-body bytes that carry one downlink payload: a single chunk of raw octets for
// stream, a base64 line chunk followed by a newline chunk for poll.
func (c sessionCodec) appendPullFrame(dst, payload []byte) []byte {
	if !c.poll {
		return appendChunk(dst, payload)
	}
	dst = strconv.AppendInt(dst, int64(base64.StdEncoding.EncodedLen(len(payload))), 16)
	dst = append(dst, '\r', '\n')
	dst = base64.StdEncoding.AppendEncode(dst, payload)
	dst = append(dst, '\r', '\n')
	return append(dst, pollNewlineChunk...)
}

func appendChunk(dst, data []byte) []byte {
	dst = strconv.AppendInt(dst, int64(len(data)), 16)
	dst = append(dst, '\r', '\n')
	dst = append(dst, data...)
	return append(dst, '\r', '\n')
}
