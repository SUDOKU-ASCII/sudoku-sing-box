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
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

// handleSession serves one stream or poll request. The two modes share their
// routing and differ only in payload encoding (codec) and in a few rejection
// details that older clients and probes can observe, so those are preserved.
func (s *TunnelServer) handleSession(req *tunnelRequest, codec sessionCodec) (HandleResult, net.Conn, error) {
	reject := func(code int, body string) (HandleResult, net.Conn, error) {
		return s.rejectOrRespond(req, code, body)
	}

	u, err := url.ParseRequestURI(req.target)
	if err != nil {
		return reject(http.StatusBadRequest, "bad request")
	}
	// Only accept plausible paths to reduce accidental exposure.
	path, ok := stripPathRoot(s.pathRoot, u.Path)
	if !ok || !isAllowedBasePath(path) {
		return reject(http.StatusNotFound, "not found")
	}
	query := u.Query()
	token := query.Get("token")

	switch strings.ToUpper(req.method) {
	case http.MethodGet:
		if token == "" && path == sessionPath {
			earlyPayload, err := parseEarlyDataQuery(query)
			if err != nil {
				return reject(http.StatusBadRequest, "bad request")
			}
			return s.sessionAuthorize(req, earlyPayload)
		}
		// A tokenless pull is a bare 400 in stream mode but reaches the session
		// lookup (403) in poll mode.
		if path != pullPath || (token == "" && !codec.poll) {
			return reject(http.StatusBadRequest, "bad request")
		}
		if s.passThroughOnReject && !s.sessions.has(token) {
			return reject(http.StatusNotFound, "not found")
		}
		return s.pull(req.conn, token, codec)

	case http.MethodPost:
		if path == uploadPath && token != "" {
			return s.handleUpload(req, query, codec)
		}
		if codec.poll {
			if path != uploadPath {
				return reject(http.StatusBadRequest, "bad request")
			}
			return reject(http.StatusBadRequest, "missing token")
		}
		return s.startStreamOne(req)

	default:
		return reject(http.StatusBadRequest, "bad request")
	}
}

// handleUpload serves POST <root>/api/v1/upload?token=...: close, FIN or a payload push.
func (s *TunnelServer) handleUpload(req *tunnelRequest, query url.Values, codec sessionCodec) (HandleResult, net.Conn, error) {
	rawConn := req.conn
	reject := func(code int, body string) (HandleResult, net.Conn, error) {
		return s.rejectOrRespond(req, code, body)
	}
	token := query.Get("token")

	if query.Get("close") == "1" {
		s.sessions.close(token)
		return replyDone(rawConn, http.StatusOK, "")
	}
	if query.Get("fin") == "1" {
		s.sessions.closeUplink(token)
		return replyDone(rawConn, http.StatusOK, "")
	}
	if s.passThroughOnReject && !s.sessions.has(token) {
		return reject(http.StatusNotFound, "not found")
	}
	body, length, err := req.body()
	if err != nil {
		if codec.poll {
			return reject(http.StatusBadRequest, "bad request")
		}
		return replyDone(rawConn, http.StatusBadRequest, "bad request")
	}
	sequence, err := parseUploadSequence(query)
	if err != nil {
		return reject(http.StatusBadRequest, "bad request")
	}
	return s.push(rawConn, token, sequence, body, length, codec)
}

// startStreamOne turns a tokenless POST into a single full-duplex tunnel.
func (s *TunnelServer) startStreamOne(req *tunnelRequest) (HandleResult, net.Conn, error) {
	rawConn := req.conn
	if err := writeTunnelResponseHeader(rawConn); err != nil {
		_ = rawConn.Close()
		return HandleDone, nil, err
	}
	bodyReader, _, err := req.body()
	if err != nil {
		_ = rawConn.Close()
		return HandleDone, nil, err
	}

	bw := bufio.NewWriterSize(rawConn, 32*1024)
	return HandleStartTunnel, &bodyConn{
		Conn:   rawConn,
		reader: bodyReader,
		writer: httputil.NewChunkedWriter(bw),
		tail:   bw,
		flush:  bw.Flush,
	}, nil
}

func parseUploadSequence(query url.Values) (uint64, error) {
	raw := strings.TrimSpace(query.Get(tunnelUploadSequenceQuery))
	if raw == "" {
		return 0, errors.New("missing upload sequence")
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, errors.New("invalid upload sequence")
	}
	return value, nil
}

func (s *TunnelServer) writeSessionUpload(token string, sess *tunnelSession, sequence uint64, payload []byte) error {
	sess.uploadMu.Lock()
	defer sess.uploadMu.Unlock()

	if !s.sessions.touch(token, sess) {
		return net.ErrClosed
	}
	switch {
	case sequence < sess.nextUploadSeq:
		// The previous request reached the server but its HTTP response was
		// lost. Treat the retried batch as an acknowledged no-op.
		return nil
	case sequence > sess.nextUploadSeq:
		return errUploadSequenceGap
	}

	if len(payload) > 0 {
		_ = sess.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		err := connutil.WriteFull(sess.conn, payload)
		_ = sess.conn.SetWriteDeadline(time.Time{})
		if err != nil {
			return err
		}
	}
	sess.nextUploadSeq++
	return nil
}

func writeUploadResult(rawConn net.Conn, err error) (HandleResult, net.Conn, error) {
	if errors.Is(err, errUploadSequenceGap) {
		return replyDone(rawConn, http.StatusConflict, "sequence gap")
	}
	return replyDone(rawConn, http.StatusGone, "gone")
}

const maxUploadBytes = 1 << 20 // per request

var (
	errUploadTooLarge    = errors.New("upload too large")
	errUploadSequenceGap = errors.New("upload sequence gap")
)

// sessionCodec is how a mode carries tunnel bytes: raw octets for stream, one
// base64 line per payload for poll.
type sessionCodec struct {
	poll bool
}

var (
	streamCodec = sessionCodec{}
	pollCodec   = sessionCodec{poll: true}
)

// readUpload reads one upload body of the given declared length (-1 when unknown) and returns the tunnel bytes
// it carries. The result lives in sc and is valid until sc is released.
func (c sessionCodec) readUpload(sc *uploadScratch, body io.Reader, length int64) ([]byte, error) {
	if !c.poll {
		payload, err := sc.readBody(body, length, maxUploadBytes+1)
		if err != nil {
			return nil, err
		}
		if len(payload) > maxUploadBytes {
			return nil, errUploadTooLarge
		}
		return payload, nil
	}

	encoded, err := sc.readBody(body, length, maxUploadBytes)
	if err != nil {
		return nil, err
	}
	return sc.decodeLines(encoded)
}

// push applies one upload request to the session and answers it.
func (s *TunnelServer) push(rawConn net.Conn, token string, sequence uint64, body io.Reader, length int64, codec sessionCodec) (HandleResult, net.Conn, error) {
	sess, ok := s.sessions.get(token)
	if !ok {
		return replyDone(rawConn, http.StatusForbidden, "forbidden")
	}

	sc := getUploadScratch()
	defer putUploadScratch(sc)
	payload, err := codec.readUpload(sc, body, length)
	if errors.Is(err, errUploadTooLarge) {
		return replyDone(rawConn, http.StatusRequestEntityTooLarge, "too large")
	}
	if err != nil {
		return replyDone(rawConn, http.StatusBadRequest, "bad request")
	}

	if err := s.writeSessionUpload(token, sess, sequence, payload); err != nil {
		if !errors.Is(err, errUploadSequenceGap) {
			s.sessions.close(token)
		}
		return writeUploadResult(rawConn, err)
	}
	return replyDone(rawConn, http.StatusOK, "")
}
