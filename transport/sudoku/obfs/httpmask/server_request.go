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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http/httputil"
	"strconv"
	"strings"
)

const maxHTTPHeaderBytes = 32 * 1024

// headerBytesSizeHint covers a typical tunnel request header in one allocation.
const headerBytesSizeHint = 512

// tunnelRequest is a parsed HTTP request. br still holds the bytes read past the header, so the body is served
// from it directly and a rejected request can be replayed byte for byte.
type tunnelRequest struct {
	conn        net.Conn
	br          *bufio.Reader
	headerBytes []byte

	method string
	target string // path + query
	proto  string
	// Header values are the first occurrence, space-trimmed; only the fields the tunnel routes on are kept.
	tunnel           string
	upgrade          string
	connection       string
	authorization    string
	transferEncoding string
	contentLength    string
	seenFields       uint8
}

// field returns the slot for a lower-case header name the tunnel cares about, plus its "seen" bit.
func (r *tunnelRequest) field(name []byte) (*string, uint8) {
	switch string(name) {
	case "x-sudoku-tunnel":
		return &r.tunnel, 1 << 0
	case "upgrade":
		return &r.upgrade, 1 << 1
	case "connection":
		return &r.connection, 1 << 2
	case "authorization":
		return &r.authorization, 1 << 3
	case "transfer-encoding":
		return &r.transferEncoding, 1 << 4
	case "content-length":
		return &r.contentLength, 1 << 5
	}
	return nil, 0
}

func (r *tunnelRequest) setField(key, value []byte) {
	// Tunnel header names are short ASCII, so lower-casing on the stack avoids an allocation per header line.
	var stack [48]byte
	lower := append(stack[:0], key...)
	for i, c := range lower {
		if c >= 0x80 {
			lower = []byte(strings.ToLower(string(key)))
			break
		}
		if 'A' <= c && c <= 'Z' {
			lower[i] = c + 'a' - 'A'
		}
	}
	slot, bit := r.field(lower)
	if slot == nil || r.seenFields&bit != 0 {
		return
	}
	r.seenFields |= bit
	*slot = strings.TrimSpace(string(value))
}

func (r *tunnelRequest) isWebSocketUpgrade() bool {
	if !strings.EqualFold(r.upgrade, "websocket") {
		return false
	}
	for _, part := range strings.Split(r.connection, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}

// buffered returns the bytes already read past the header. They are only valid until the body is read.
func (r *tunnelRequest) buffered() []byte {
	b, _ := r.br.Peek(r.br.Buffered())
	return b
}

// replayPrefix returns the request exactly as received so far. Call it only before the body is consumed.
func (r *tunnelRequest) replayPrefix() []byte {
	buffered := r.buffered()
	out := make([]byte, 0, len(r.headerBytes)+len(buffered))
	out = append(out, r.headerBytes...)
	return append(out, buffered...)
}

// body returns a reader over the request body, which follows the header in br, and its declared length (-1 when
// unknown).
func (r *tunnelRequest) body() (io.Reader, int64, error) {
	if strings.Contains(strings.ToLower(r.transferEncoding), "chunked") {
		return httputil.NewChunkedReader(r.br), -1, nil
	}
	if r.contentLength != "" {
		n, err := strconv.ParseInt(r.contentLength, 10, 64)
		if err != nil || n < 0 {
			return nil, 0, fmt.Errorf("invalid content-length")
		}
		return io.LimitReader(r.br, n), n, nil
	}
	return r.br, -1, nil
}

// readHTTPRequest parses a request header from br (which already holds first, the sniffed leading bytes). On
// failure the second result is everything read so far, ready to be replayed to the next protocol handler.
func readHTTPRequest(conn net.Conn, br *bufio.Reader, first []byte) (*tunnelRequest, []byte, error) {
	consumed := make([]byte, 0, headerBytesSizeHint)
	fail := func(err error) (*tunnelRequest, []byte, error) {
		return nil, buildInvalidHTTPReplayPrefix(first, consumed, readAllBuffered(br)), err
	}
	readLine := func() ([]byte, error) {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			if len(consumed)+len(line) > maxHTTPHeaderBytes {
				return line, fmt.Errorf("http header too large")
			}
			consumed = append(consumed, line...)
		}
		return line, err
	}

	line, err := readLine()
	if err != nil {
		return fail(err)
	}
	requestLine := strings.TrimRight(string(line), "\r\n")
	method, rest, ok := strings.Cut(requestLine, " ")
	if !ok {
		return fail(fmt.Errorf("invalid request line"))
	}
	target, proto, ok := strings.Cut(rest, " ")
	if !ok {
		return fail(fmt.Errorf("invalid request line"))
	}
	req := &tunnelRequest{conn: conn, br: br, method: method, target: target, proto: proto}

	for {
		line, err = readLine()
		if err != nil {
			return fail(err)
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			break
		}
		key, value, ok := bytes.Cut(line, []byte{':'})
		if !ok {
			continue
		}
		if key = bytes.TrimSpace(key); len(key) > 0 {
			req.setField(key, value)
		}
	}
	req.headerBytes = consumed
	return req, nil, nil
}

func buildInvalidHTTPReplayPrefix(first, headerBytes, buffered []byte) []byte {
	// readHTTPRequest may have consumed some bytes that don't include our initial 4-byte peek
	// (e.g. parse errors / short reads). Preserve a correct replay prefix for downstream handlers.
	out := make([]byte, 0, len(first)+len(headerBytes)+len(buffered))
	if len(headerBytes) == 0 || !bytes.HasPrefix(headerBytes, first) {
		out = append(out, first...)
	}
	out = append(out, headerBytes...)
	out = append(out, buffered...)
	return out
}

func readAllBuffered(r *bufio.Reader) []byte {
	n := r.Buffered()
	if n <= 0 {
		return nil
	}
	b, err := r.Peek(n)
	if err != nil {
		return nil
	}
	out := make([]byte, n)
	copy(out, b)
	return out
}

func isAllowedBasePath(path string) bool {
	for _, p := range paths {
		if path == p {
			return true
		}
	}
	return false
}
