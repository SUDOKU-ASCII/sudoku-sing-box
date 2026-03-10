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
	crand "crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	userAgents      = splitLines(userAgentsData)
	accepts         = splitLines(acceptsData)
	acceptLanguages = splitLines(acceptLanguagesData)
	acceptEncodings = splitLines(acceptEncodingsData)
	paths           = splitLines(pathsData)
	contentTypes    = splitLines(contentTypesData)
)

var (
	rngPool = sync.Pool{
		New: func() interface{} {
			return rand.New(rand.NewSource(newSeed()))
		},
	}
	headerBufPool = sync.Pool{
		New: func() interface{} {
			b := make([]byte, 0, 1024)
			return &b
		},
	}
)

//go:embed masker_user_agents.txt
var userAgentsData string

//go:embed masker_accepts.txt
var acceptsData string

//go:embed masker_accept_languages.txt
var acceptLanguagesData string

//go:embed masker_accept_encodings.txt
var acceptEncodingsData string

//go:embed masker_paths.txt
var pathsData string

//go:embed masker_content_types.txt
var contentTypesData string

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	j := 0
	for _, v := range out {
		if v == "" {
			continue
		}
		out[j] = v
		j++
	}
	return out[:j]
}

func newSeed() int64 {
	seed := time.Now().UnixNano()
	var b [8]byte
	if _, err := crand.Read(b[:]); err == nil {
		seed = int64(binary.BigEndian.Uint64(b[:]))
	}
	return seed
}

// LooksLikeHTTPRequestStart reports whether peek4 looks like a supported HTTP/1.x request method prefix.
func LooksLikeHTTPRequestStart(peek4 []byte) bool {
	if len(peek4) < 4 {
		return false
	}
	// Common methods: "GET ", "POST", "HEAD", "PUT ", "OPTI" (OPTIONS), "PATC" (PATCH), "DELE" (DELETE)
	return bytes.Equal(peek4, []byte("GET ")) ||
		bytes.Equal(peek4, []byte("POST")) ||
		bytes.Equal(peek4, []byte("HEAD")) ||
		bytes.Equal(peek4, []byte("PUT ")) ||
		bytes.Equal(peek4, []byte("OPTI")) ||
		bytes.Equal(peek4, []byte("PATC")) ||
		bytes.Equal(peek4, []byte("DELE"))
}

func trimPortForHost(host string) string {
	if host == "" {
		return host
	}
	// Accept "example.com:443" / "1.2.3.4:443" / "[::1]:443"
	h, _, err := net.SplitHostPort(host)
	if err == nil && h != "" {
		return h
	}
	// If it's not in host:port form, keep as-is.
	return host
}

func appendCommonHeaders(buf []byte, host string, r *rand.Rand) []byte {
	ua := userAgents[r.Intn(len(userAgents))]
	accept := accepts[r.Intn(len(accepts))]
	lang := acceptLanguages[r.Intn(len(acceptLanguages))]
	enc := acceptEncodings[r.Intn(len(acceptEncodings))]

	buf = append(buf, "Host: "...)
	buf = append(buf, host...)
	buf = append(buf, "\r\nUser-Agent: "...)
	buf = append(buf, ua...)
	buf = append(buf, "\r\nAccept: "...)
	buf = append(buf, accept...)
	buf = append(buf, "\r\nAccept-Language: "...)
	buf = append(buf, lang...)
	buf = append(buf, "\r\nAccept-Encoding: "...)
	buf = append(buf, enc...)
	buf = append(buf, "\r\nConnection: keep-alive\r\n"...)

	// A couple of common cache headers; keep them static for simplicity.
	buf = append(buf, "Cache-Control: no-cache\r\nPragma: no-cache\r\n"...)
	return buf
}

// WriteRandomRequestHeader writes a plausible HTTP/1.1 request header as a mask.
func WriteRandomRequestHeader(w io.Writer, host string) error {
	return WriteRandomRequestHeaderWithPathRoot(w, host, "")
}

// WriteRandomRequestHeaderWithPathRoot is like WriteRandomRequestHeader but prefixes all paths with pathRoot
// (a single segment such as "aabbcc" => "/aabbcc/...").
func WriteRandomRequestHeaderWithPathRoot(w io.Writer, host string, pathRoot string) error {
	// Get RNG from pool
	r := rngPool.Get().(*rand.Rand)
	defer rngPool.Put(r)

	path := joinPathRoot(pathRoot, paths[r.Intn(len(paths))])
	ctype := contentTypes[r.Intn(len(contentTypes))]

	// Use buffer pool
	bufPtr := headerBufPool.Get().(*[]byte)
	buf := *bufPtr
	buf = buf[:0]
	defer func() {
		if cap(buf) <= 4096 {
			*bufPtr = buf
			headerBufPool.Put(bufPtr)
		}
	}()

	// Weighted template selection. Keep a conservative default (POST w/ Content-Length),
	// but occasionally rotate to other realistic templates (e.g. WebSocket upgrade).
	switch r.Intn(10) {
	case 0, 1: // ~20% WebSocket-like upgrade
		hostNoPort := trimPortForHost(host)
		var keyBytes [16]byte
		_, _ = crand.Read(keyBytes[:])
		wsKey := base64.StdEncoding.EncodeToString(keyBytes[:])

		buf = append(buf, "GET "...)
		buf = append(buf, path...)
		buf = append(buf, " HTTP/1.1\r\n"...)
		buf = appendCommonHeaders(buf, host, r)
		buf = append(buf, "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: "...)
		buf = append(buf, wsKey...)
		buf = append(buf, "\r\nOrigin: https://"...)
		buf = append(buf, hostNoPort...)
		buf = append(buf, "\r\n\r\n"...)
	default: // ~80% POST upload
		// Random Content-Length: 4KB–10MB. Small enough to look plausible, large enough
		// to justify long-lived writes on keep-alive connections.
		const minCL = int64(4 * 1024)
		const maxCL = int64(10 * 1024 * 1024)
		contentLength := minCL + r.Int63n(maxCL-minCL+1)

		buf = append(buf, "POST "...)
		buf = append(buf, path...)
		buf = append(buf, " HTTP/1.1\r\n"...)
		buf = appendCommonHeaders(buf, host, r)
		buf = append(buf, "Content-Type: "...)
		buf = append(buf, ctype...)
		buf = append(buf, "\r\nContent-Length: "...)
		buf = strconv.AppendInt(buf, contentLength, 10)
		// A couple of extra headers seen in real clients.
		if r.Intn(2) == 0 {
			buf = append(buf, "\r\nX-Requested-With: XMLHttpRequest"...)
		}
		if r.Intn(3) == 0 {
			buf = append(buf, "\r\nReferer: https://"...)
			buf = append(buf, trimPortForHost(host)...)
			buf = append(buf, "/"...)
		}
		buf = append(buf, "\r\n\r\n"...)
	}

	_, err := w.Write(buf)
	return err
}

// ConsumeHeader reads and consumes the HTTP header, returning the consumed bytes.
// Returns an error if the request is not a recognized HTTP method or is badly malformed.
func ConsumeHeader(r *bufio.Reader) ([]byte, error) {
	var consumed bytes.Buffer

	// 1. Read the request line
	// Use ReadSlice to avoid allocation if line fits in buffer
	line, err := r.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	consumed.Write(line)

	// Basic method validation: accept common HTTP/1.x methods used by our masker.
	// Keep it strict enough to reject obvious garbage.
	switch {
	case bytes.HasPrefix(line, []byte("POST ")),
		bytes.HasPrefix(line, []byte("GET ")),
		bytes.HasPrefix(line, []byte("HEAD ")),
		bytes.HasPrefix(line, []byte("PUT ")),
		bytes.HasPrefix(line, []byte("DELETE ")),
		bytes.HasPrefix(line, []byte("OPTIONS ")),
		bytes.HasPrefix(line, []byte("PATCH ")):
	default:
		return consumed.Bytes(), fmt.Errorf("invalid method or garbage: %s", strings.TrimSpace(string(line)))
	}

	// 2. Read headers until an empty line is encountered
	for {
		line, err = r.ReadSlice('\n')
		if err != nil {
			return consumed.Bytes(), err
		}
		consumed.Write(line)

		// Check for empty line (\r\n or \n)
		// ReadSlice includes the delimiter
		n := len(line)
		if n == 2 && line[0] == '\r' && line[1] == '\n' {
			return consumed.Bytes(), nil
		}
		if n == 1 && line[0] == '\n' {
			return consumed.Bytes(), nil
		}
	}
}
