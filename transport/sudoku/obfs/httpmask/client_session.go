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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type sessionDialInfo struct {
	client     *http.Client
	pushURL    string
	pullURL    string
	finURL     string
	closeURL   string
	headerHost string
}

func dialSession(ctx context.Context, serverAddress string, opts TunnelDialOptions, mode TunnelMode) (*sessionDialInfo, error) {
	scheme, urlHost, dialAddr, serverName, err := normalizeHTTPDialTarget(serverAddress, opts.TLSEnabled, opts.HostOverride)
	if err != nil {
		return nil, err
	}
	headerHost := canonicalHeaderHost(urlHost, scheme)
	httpClient := newHTTPClient(urlHost, dialAddr, serverName, scheme, 32, multiplexEnabled(opts.Multiplex), opts.TransportPool, opts.DialContext)

	endpoint := func(path, rawQuery string) string {
		return (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, path), RawQuery: rawQuery}).String()
	}

	authorizeURL := endpoint(sessionPath, "")
	if opts.EarlyHandshake != nil && len(opts.EarlyHandshake.RequestPayload) > 0 {
		authorizeURL, err = setEarlyDataQuery(authorizeURL, opts.EarlyHandshake.RequestPayload)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
	if err != nil {
		return nil, err
	}
	req.Host = headerHost
	applyTunnelHeaders(req.Header, headerHost, mode)

	cancelPreconnect := httpClient.preconnect(ctx, req, tunnelPreconnectCount)
	keepPreconnected := false
	defer func() {
		if !keepPreconnected {
			cancelPreconnect()
		}
	}()

	resp, err := httpClient.client.Do(req)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s authorize bad status: %s (%s)", mode, resp.Status, strings.TrimSpace(string(bodyBytes)))
	}

	authResp, err := parseAuthorizeResponse(bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%s authorize failed: %w", mode, err)
	}
	token := authResp.token
	if opts.EarlyHandshake != nil && len(authResp.earlyPayload) > 0 && opts.EarlyHandshake.HandleResponse != nil {
		if err := opts.EarlyHandshake.HandleResponse(authResp.earlyPayload); err != nil {
			return nil, err
		}
	}

	tokenQuery := "token=" + url.QueryEscape(token)
	pushURL := endpoint(uploadPath, tokenQuery)
	pullURL := endpoint(pullPath, tokenQuery)
	finURL := endpoint(uploadPath, tokenQuery+"&fin=1")
	closeURL := endpoint(uploadPath, tokenQuery+"&close=1")
	keepPreconnected = true

	return &sessionDialInfo{
		client:     httpClient.client,
		pushURL:    pushURL,
		pullURL:    pullURL,
		finURL:     finURL,
		closeURL:   closeURL,
		headerHost: headerHost,
	}, nil
}

const (
	controlMaxAttempts    = 3
	controlTimeout        = 5 * time.Second
	controlInitialBackoff = 50 * time.Millisecond
)

// sendSessionControl posts a fin/close request, retrying transient failures.
func sendSessionControl(client *http.Client, controlURL, headerHost string, mode TunnelMode) error {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()

	backoff := controlInitialBackoff
	for attempt := 1; ; attempt++ {
		retryable, err := postSessionControl(ctx, client, controlURL, headerHost, mode)
		if err == nil || !retryable || attempt >= controlMaxAttempts {
			return err
		}
		if err := waitRetry(ctx.Done(), nil, backoff); err != nil {
			return err
		}
		backoff *= 2
	}
}

func postSessionControl(ctx context.Context, client *http.Client, controlURL, headerHost string, mode TunnelMode) (retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, nil)
	if err != nil {
		return false, err
	}
	req.Host = headerHost
	applyTunnelHeaders(req.Header, headerHost, mode)

	resp, err := client.Do(req)
	if err != nil {
		retryable = ctx.Err() == nil && (isDialError(err) || isRetryableHTTPTransportError(err) || errors.Is(err, context.DeadlineExceeded))
		return retryable, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return false, nil
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		// The server already discarded the session, so closing it again succeeded.
		return false, nil
	}
	return isRetryableStatusCode(resp.StatusCode), statusError(resp)
}
