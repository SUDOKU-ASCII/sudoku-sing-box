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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

func dialStream(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	// "stream" mode uses split-stream to stay CDN-friendly by default.
	return dialStreamSplit(ctx, serverAddress, opts)
}

type streamSplitConn struct {
	queuedConn
	readiness *tunnelReadiness

	ctx    context.Context
	cancel context.CancelFunc

	client     *http.Client
	pushURL    string
	pullURL    string
	finURL     string
	closeURL   string
	headerHost string
	auth       *tunnelAuth
	waitSpare  func(context.Context) error
}

func (c *streamSplitConn) Close() error {
	return c.closeWithError(io.ErrClosedPipe)
}

func (c *streamSplitConn) closeWithError(err error) error {
	_ = c.queuedConn.closeWithError(err)
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

	connCtx, cancel := context.WithCancel(context.Background())
	c := &streamSplitConn{
		ctx:        connCtx,
		cancel:     cancel,
		readiness:  newTunnelReadiness(),
		client:     info.client,
		pushURL:    info.pushURL,
		pullURL:    info.pullURL,
		finURL:     info.finURL,
		closeURL:   info.closeURL,
		headerHost: info.headerHost,
		auth:       info.auth,
		queuedConn: newQueuedConn(),
	}

	c.waitSpare = configureSplitPreconnect(connCtx, info, opts.Multiplex, c.closed)
	go c.pullLoop()
	go c.pushLoop()
	return finishTunnelDial(c, opts, c.waitReady)
}

func (c *streamSplitConn) waitReady(ctx context.Context) error {
	return waitSplitTunnelReady(ctx, c.readiness, c.closed, c.closedErr, c.waitSpare)
}

func (c *streamSplitConn) pullLoop() {
	const (
		readChunkSize = 32 * 1024
		idleBackoff   = 25 * time.Millisecond
		maxDialRetry  = 12
		minBackoff    = 10 * time.Millisecond
		maxBackoff    = 250 * time.Millisecond
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

		// The server ends an idle pull after PullReadTimeout. A fixed client
		// lifetime would instead cancel healthy continuous downloads mid-stream.
		reqCtx, cancel := context.WithCancel(c.ctx)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.pullURL, nil)
		if err != nil {
			cancel()
			_ = c.Close()
			return
		}
		req.Host = c.headerHost
		applyTunnelHeaders(req.Header, c.headerHost, TunnelModeStream)
		applyTunnelAuth(req, c.auth, TunnelModeStream, http.MethodGet, "/stream")
		// Stream pulls are long-lived and the raw server closes the carrier after
		// the terminal chunk. Do not race that close against transport reuse.
		req.Close = true

		resp, err := c.client.Do(req)
		if err != nil {
			cancel()
			if (isDialError(err) || isRetryableHTTPTransportError(err)) && dialRetry < maxDialRetry {
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
			_ = c.Close()
			return
		}
		dialRetry = 0
		backoff = minBackoff

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			cancel()
			_ = c.Close()
			return
		}
		c.readiness.markPullReady()

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
					if resp.Trailer.Get(tunnelStreamEOFHeader) == "1" {
						c.markReadEOF()
						return
					}
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

func (c *streamSplitConn) pushLoop() {
	const (
		maxBatchBytes  = 256 * 1024
		flushInterval  = 5 * time.Millisecond
		requestTimeout = 20 * time.Second
		maxDialRetry   = 12
		minBackoff     = 10 * time.Millisecond
		maxBackoff     = 250 * time.Millisecond
	)

	var (
		buf      bytes.Buffer
		timer    = time.NewTimer(flushInterval)
		writeErr error
	)
	defer timer.Stop()
	defer func() { c.completeWrite(writeErr) }()

	fail := func(err error) {
		writeErr = err
		_ = c.closeWithError(err)
	}

	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}

		reqCtx, cancel := context.WithTimeout(c.ctx, requestTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.pushURL, bytes.NewReader(buf.Bytes()))
		if err != nil {
			cancel()
			return err
		}
		req.Host = c.headerHost
		applyTunnelHeaders(req.Header, c.headerHost, TunnelModeStream)
		applyTunnelAuth(req, c.auth, TunnelModeStream, http.MethodPost, "/api/v1/upload")
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := c.client.Do(req)
		if err != nil {
			cancel()
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("bad status: %s", resp.Status)
		}

		buf.Reset()
		c.readiness.markPushReady()
		return nil
	}

	flushWithRetry := func() error {
		return retryDial(c.closed, c.closedErr, maxDialRetry, minBackoff, maxBackoff, flush)
	}
	resetTimer(timer, flushInterval)

	enqueue := func(b []byte) error {
		for len(b) > 0 {
			space := maxBatchBytes - buf.Len()
			if space == 0 {
				if err := flushWithRetry(); err != nil {
					return err
				}
				resetTimer(timer, flushInterval)
				space = maxBatchBytes
			}
			if len(b) < space {
				space = len(b)
			}
			_, _ = buf.Write(b[:space])
			b = b[space:]
		}
		return nil
	}

	for {
		select {
		case b := <-c.writeCh:
			if len(b) == 0 {
				continue
			}
			if err := enqueue(b); err != nil {
				fail(fmt.Errorf("stream push flush failed: %w", err))
				return
			}
			if buf.Len() >= maxBatchBytes {
				if err := flushWithRetry(); err != nil {
					fail(fmt.Errorf("stream push flush failed: %w", err))
					return
				}
				resetTimer(timer, flushInterval)
			}
		case <-timer.C:
			if err := flushWithRetry(); err != nil {
				fail(fmt.Errorf("stream push flush failed: %w", err))
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
					if err := enqueue(b); err != nil {
						fail(fmt.Errorf("stream push flush failed: %w", err))
						return
					}
				default:
					if err := flushWithRetry(); err != nil {
						fail(fmt.Errorf("stream push flush failed: %w", err))
						return
					}
					if err := sendSessionControl(c.client, c.finURL, c.headerHost, TunnelModeStream, c.auth); err != nil {
						fail(fmt.Errorf("stream FIN failed: %w", err))
						return
					}
					return
				}
			}
		case <-c.closed:
			writeErr = c.closedErr()
			return
		}
	}
}
