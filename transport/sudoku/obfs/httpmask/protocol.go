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
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Wire vocabulary shared by the HTTPMask client and server.
const (
	tunnelEarlyDataQueryKey   = "ed"
	tunnelEarlyDataHeader     = "X-Sudoku-Early"
	tunnelStreamEOFHeader     = "X-Sudoku-Stream-EOF"
	tunnelUploadSequenceQuery = "seq"
	tunnelUploadSequenceCap   = "upload-seq"

	sessionPath = "/session"
	pullPath    = "/stream"
	uploadPath  = "/api/v1/upload"
)

type authorizeResponse struct {
	token        string
	earlyPayload []byte
}

func parseAuthorizeResponse(body []byte) (*authorizeResponse, error) {
	s := strings.TrimSpace(string(body))
	idx := strings.Index(s, "token=")
	if idx < 0 {
		return nil, errors.New("missing token")
	}
	s = s[idx+len("token="):]
	if s == "" {
		return nil, errors.New("empty token")
	}
	// Token is base64.RawURLEncoding (A-Z a-z 0-9 - _). Strip any trailing bytes (e.g. from CDN compression).
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteByte(c)
			continue
		}
		break
	}
	token := b.String()
	if token == "" {
		return nil, errors.New("empty token")
	}
	out := &authorizeResponse{token: token}
	if earlyLine := findAuthorizeField(body, "ed="); earlyLine != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(earlyLine)
		if err != nil {
			return nil, fmt.Errorf("decode early authorize payload failed: %w", err)
		}
		out.earlyPayload = decoded
	}
	if findAuthorizeField(body, "cap=") != tunnelUploadSequenceCap {
		return nil, errors.New("server does not support HTTPMask v0.5 upload sequencing")
	}
	return out, nil
}

func findAuthorizeField(body []byte, prefix string) string {
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func setEarlyDataQuery(rawURL string, payload []byte) (string, error) {
	if len(payload) == 0 {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(tunnelEarlyDataQueryKey, base64.RawURLEncoding.EncodeToString(payload))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseEarlyDataQuery(query url.Values) ([]byte, error) {
	val := strings.TrimSpace(query.Get(tunnelEarlyDataQueryKey))
	if val == "" {
		return nil, nil
	}
	return base64.RawURLEncoding.DecodeString(val)
}

// uploadURL puts the sequence ahead of pushURL's query, where query encoding (sorted keys) has always placed it.
func uploadURL(pushURL string, sequence uint64) string {
	base, query, _ := strings.Cut(pushURL, "?")
	return base + "?" + tunnelUploadSequenceQuery + "=" + strconv.FormatUint(sequence, 10) + "&" + query
}
