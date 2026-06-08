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
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func dialStream(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	// "stream" mode uses XHTTP-style packet-up: a long downlink GET plus
	// packetized uplink POSTs keyed by a client-generated session id.
	return dialStreamPacketUp(ctx, serverAddress, opts)
}

type streamConn struct {
	queuedConn

	ctx    context.Context
	cancel context.CancelFunc

	client     *http.Client
	pushURL    string
	pullURL    string
	finURL     string
	closeURL   string
	headerHost string
	auth       *tunnelAuth

	early     *ClientEarlyHandshake
	earlyDone chan error
	earlyOnce sync.Once
}

func (c *streamConn) Close() error {
	_ = c.closeWithError(io.ErrClosedPipe)

	if c.cancel != nil {
		c.cancel()
	}

	bestEffortCloseSession(c.client, c.closeURL, c.headerHost, TunnelModeStream, c.auth)
	return nil
}

func dialStreamSplit(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	info, err := dialSession(ctx, serverAddress, opts, TunnelModeStream)
	if err != nil {
		return nil, err
	}

	return newStreamSplitConn(info, opts)
}

func dialStreamPacketUp(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	info, err := newPacketUpSession(serverAddress, opts)
	if err != nil {
		return nil, err
	}

	return newStreamPacketConn(ctx, info, opts)
}

func newPacketUpSession(serverAddress string, opts TunnelDialOptions) (*sessionDialInfo, error) {
	scheme, urlHost, dialAddr, serverName, err := normalizeHTTPDialTarget(serverAddress, opts.TLSEnabled, opts.HostOverride)
	if err != nil {
		return nil, err
	}
	headerHost := canonicalHeaderHost(urlHost, scheme)
	auth := newTunnelAuth(opts.AuthKey, 0)
	client := newHTTPClient(urlHost, dialAddr, serverName, scheme, 32, multiplexEnabled(opts.Multiplex), opts.TransportPool, opts.DialContext)

	token, err := newSessionToken()
	if err != nil {
		return nil, err
	}
	query := "token=" + url.QueryEscape(token)
	pullURL := (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/stream"), RawQuery: query}).String()
	if opts.EarlyHandshake != nil && len(opts.EarlyHandshake.RequestPayload) > 0 {
		pullURL, err = setEarlyDataQuery(pullURL, opts.EarlyHandshake.RequestPayload)
		if err != nil {
			return nil, err
		}
	}
	return &sessionDialInfo{
		client:     client,
		pushURL:    (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/api/v1/upload"), RawQuery: query}).String(),
		pullURL:    pullURL,
		finURL:     (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/api/v1/upload"), RawQuery: query + "&fin=1"}).String(),
		closeURL:   (&url.URL{Scheme: scheme, Host: urlHost, Path: joinPathRoot(opts.PathRoot, "/api/v1/upload"), RawQuery: query + "&close=1"}).String(),
		headerHost: headerHost,
		auth:       auth,
	}, nil
}

func newStreamSplitConn(info *sessionDialInfo, opts TunnelDialOptions) (net.Conn, error) {
	c := newStreamConn(info)

	go c.pullLoop()
	go c.pushLoop()
	outConn := net.Conn(c)
	if opts.EarlyHandshake != nil && opts.EarlyHandshake.WrapConn != nil && (opts.EarlyHandshake.Ready == nil || opts.EarlyHandshake.Ready()) {
		upgraded, err := opts.EarlyHandshake.WrapConn(c)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		if upgraded != nil {
			outConn = upgraded
		}
		return outConn, nil
	}
	if opts.Upgrade != nil {
		upgraded, err := opts.Upgrade(c)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		if upgraded != nil {
			outConn = upgraded
		}
	}
	return outConn, nil
}

func newStreamPacketConn(ctx context.Context, info *sessionDialInfo, opts TunnelDialOptions) (net.Conn, error) {
	c := newStreamConn(info)
	if opts.EarlyHandshake != nil && len(opts.EarlyHandshake.RequestPayload) > 0 {
		c.early = opts.EarlyHandshake
		c.earlyDone = make(chan error, 1)
	}

	go c.pullLoop()
	go c.pushLoop()

	outConn := net.Conn(c)
	if c.earlyDone != nil {
		select {
		case err := <-c.earlyDone:
			if err != nil {
				_ = c.Close()
				return nil, err
			}
		case <-ctx.Done():
			_ = c.Close()
			return nil, ctx.Err()
		}
		if opts.EarlyHandshake.WrapConn != nil && (opts.EarlyHandshake.Ready == nil || opts.EarlyHandshake.Ready()) {
			upgraded, err := opts.EarlyHandshake.WrapConn(c)
			if err != nil {
				_ = c.Close()
				return nil, err
			}
			if upgraded != nil {
				outConn = upgraded
			}
			return outConn, nil
		}
	}
	if opts.Upgrade != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = c.SetReadDeadline(deadline)
		}
		upgraded, err := opts.Upgrade(c)
		_ = c.SetReadDeadline(time.Time{})
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		if upgraded != nil {
			outConn = upgraded
		}
	}
	return outConn, nil
}

func newStreamConn(info *sessionDialInfo) *streamConn {
	connCtx, cancel := context.WithCancel(context.Background())
	return &streamConn{
		ctx:        connCtx,
		cancel:     cancel,
		client:     info.client,
		pushURL:    info.pushURL,
		pullURL:    info.pullURL,
		finURL:     info.finURL,
		closeURL:   info.closeURL,
		headerHost: info.headerHost,
		auth:       info.auth,
		queuedConn: queuedConn{
			rxc:         make(chan []byte, queuedConnPayloadQueueDepth),
			closed:      make(chan struct{}),
			writeCh:     make(chan []byte, queuedConnPayloadQueueDepth),
			writeClosed: make(chan struct{}),
			localAddr:   &net.TCPAddr{},
			remoteAddr:  &net.TCPAddr{},
		},
	}
}

func (c *streamConn) signalEarly(err error) {
	if c == nil || c.earlyDone == nil {
		return
	}
	c.earlyOnce.Do(func() {
		c.earlyDone <- err
	})
}

func (c *streamConn) handleEarlyResponse(h http.Header) error {
	if c == nil || c.early == nil || c.early.HandleResponse == nil {
		return nil
	}
	val := strings.TrimSpace(h.Get(tunnelEarlyDataHeader))
	if val == "" {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(val)
	if err != nil {
		return fmt.Errorf("decode early response failed: %w", err)
	}
	if err := c.early.HandleResponse(payload); err != nil {
		return err
	}
	return nil
}

func (c *streamConn) pullLoop() {
	const (
		// requestTimeout must be long enough for continuous high-throughput streams (e.g. mux + large downloads).
		// If it is too short, the client cancels the response mid-body and corrupts the byte stream.
		requestTimeout = 2 * time.Minute
		readChunkSize  = 32 * 1024
		idleBackoff    = 25 * time.Millisecond
		maxDialRetry   = 12
		minBackoff     = 10 * time.Millisecond
		maxBackoff     = 250 * time.Millisecond
	)

	var (
		dialRetry int
		backoff   = minBackoff
	)
	buf := make([]byte, readChunkSize)
	for {
		select {
		case <-c.closed:
			return
		default:
		}

		reqCtx, cancel := context.WithTimeout(c.ctx, requestTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.pullURL, nil)
		if err != nil {
			cancel()
			_ = c.Close()
			return
		}
		req.Host = c.headerHost
		applyTunnelHeaders(req.Header, c.headerHost, TunnelModeStream)
		applyTunnelAuth(req, c.auth, TunnelModeStream, http.MethodGet, "/stream")

		resp, err := c.client.Do(req)
		if err != nil {
			cancel()
			if isDialError(err) && dialRetry < maxDialRetry {
				dialRetry++
				select {
				case <-time.After(backoff):
				case <-c.closed:
					return
				}
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			c.signalEarly(err)
			_ = c.Close()
			return
		}
		dialRetry = 0
		backoff = minBackoff

		if resp.StatusCode != http.StatusOK {
			c.signalEarly(fmt.Errorf("stream pull bad status: %s", resp.Status))
			_ = resp.Body.Close()
			cancel()
			_ = c.Close()
			return
		}
		c.signalEarly(c.handleEarlyResponse(resp.Header))

		readAny := false
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				readAny = true
				payload := make([]byte, n)
				copy(payload, buf[:n])
				select {
				case c.rxc <- payload:
				case <-c.closed:
					_ = resp.Body.Close()
					cancel()
					return
				}
			}
			if rerr != nil {
				_ = resp.Body.Close()
				cancel()
				if errors.Is(rerr, io.EOF) {
					// Long-poll ended; retry.
					break
				}
				_ = c.Close()
				return
			}
		}
		cancel()
		if !readAny {
			// Avoid tight loop if the server replied quickly with an empty body.
			select {
			case <-time.After(idleBackoff):
			case <-c.closed:
				return
			}
		}
	}
}

func (c *streamConn) pushLoop() {
	const (
		maxBatchBytes  = 256 * 1024
		flushInterval  = 2 * time.Millisecond
		maxUploadPosts = 32
		requestTimeout = 20 * time.Second
		maxDialRetry   = 12
		minBackoff     = 10 * time.Millisecond
		maxBackoff     = 250 * time.Millisecond
	)

	var (
		buf         bytes.Buffer
		timer       = time.NewTimer(flushInterval)
		nextSeq     uint64
		uploadSlots = make(chan struct{}, maxUploadPosts)
		uploadWG    sync.WaitGroup
	)
	defer timer.Stop()

	post := func(seq uint64, payload []byte) error {
		reqCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, urlWithQueryValue(c.pushURL, "seq", strconv.FormatUint(seq, 10)), bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Host = c.headerHost
		applyTunnelHeaders(req.Header, c.headerHost, TunnelModeStream)
		applyTunnelAuth(req, c.auth, TunnelModeStream, http.MethodPost, "/api/v1/upload")
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("bad status: %s", resp.Status)
		}
		return nil
	}

	postAsync := func(seq uint64, payload []byte) error {
		select {
		case uploadSlots <- struct{}{}:
		case <-c.closed:
			return io.ErrClosedPipe
		}
		uploadWG.Add(1)
		go func() {
			defer uploadWG.Done()
			defer func() { <-uploadSlots }()
			err := retryDial(c.closed, func() error { return io.ErrClosedPipe }, maxDialRetry, minBackoff, maxBackoff, func() error {
				return post(seq, payload)
			})
			if err != nil {
				_ = c.Close()
			}
		}()
		return nil
	}

	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		payload := append([]byte(nil), buf.Bytes()...)
		buf.Reset()
		seq := nextSeq
		nextSeq++
		return postAsync(seq, payload)
	}

	waitUploads := func() {
		done := make(chan struct{})
		go func() {
			uploadWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(requestTimeout):
			_ = c.Close()
		}
	}
	resetTimer(timer, flushInterval)

	for {
		select {
		case b, ok := <-c.writeCh:
			if !ok {
				_ = flush()
				waitUploads()
				return
			}
			if len(b) == 0 {
				continue
			}
			if buf.Len()+len(b) > maxBatchBytes {
				if err := flush(); err != nil {
					_ = c.Close()
					return
				}
				resetTimer(timer, flushInterval)
			}
			_, _ = buf.Write(b)
			if buf.Len() >= maxBatchBytes {
				if err := flush(); err != nil {
					_ = c.Close()
					return
				}
				resetTimer(timer, flushInterval)
			}
		case <-timer.C:
			if err := flush(); err != nil {
				_ = c.Close()
				return
			}
			resetTimer(timer, flushInterval)
		case <-c.writeClosed:
			// Drain any already-accepted writes so CloseWrite does not lose data.
			for {
				select {
				case b := <-c.writeCh:
					if len(b) == 0 {
						continue
					}
					if buf.Len()+len(b) > maxBatchBytes {
						if err := flush(); err != nil {
							_ = c.Close()
							return
						}
					}
					_, _ = buf.Write(b)
				default:
					_ = flush()
					waitUploads()
					bestEffortCloseSession(c.client, c.finURL, c.headerHost, TunnelModeStream, c.auth)
					return
				}
			}
		case <-c.closed:
			_ = flush()
			return
		}
	}
}

func urlWithQueryValue(rawURL, key, value string) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + url.QueryEscape(key) + "=" + url.QueryEscape(value)
}
