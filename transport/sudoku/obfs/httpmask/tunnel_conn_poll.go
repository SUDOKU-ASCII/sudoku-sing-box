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
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type pollConn struct {
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

func (c *pollConn) closeWithError(err error) error {
	_ = c.queuedConn.closeWithError(err)
	if c.cancel != nil {
		c.cancel()
	}
	bestEffortCloseSession(c.client, c.closeURL, c.headerHost, TunnelModePoll, c.auth)
	return nil
}

func (c *pollConn) Close() error {
	return c.closeWithError(io.ErrClosedPipe)
}

func dialPoll(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	info, err := dialSession(ctx, serverAddress, opts, TunnelModePoll)
	if err != nil {
		return nil, err
	}

	connCtx, cancel := context.WithCancel(context.Background())
	c := &pollConn{
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

func (c *pollConn) waitReady(ctx context.Context) error {
	return waitSplitTunnelReady(ctx, c.readiness, c.closed, c.closedErr, c.waitSpare)
}

func (c *pollConn) pullLoop() {
	const (
		maxDialRetry = 12
		minBackoff   = 10 * time.Millisecond
		maxBackoff   = 250 * time.Millisecond
	)

	var (
		dialRetry int
		backoff   = minBackoff
	)
	for {
		select {
		case <-c.closed:
			return
		default:
		}

		req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.pullURL, nil)
		if err != nil {
			_ = c.closeWithError(err)
			return
		}
		req.Host = c.headerHost
		applyTunnelHeaders(req.Header, c.headerHost, TunnelModePoll)
		applyTunnelAuth(req, c.auth, TunnelModePoll, http.MethodGet, "/stream")
		// Each poll response ends its carrier, even when the server advertises
		// keep-alive for compatibility. Keep it out of the shared idle pool.
		req.Close = true

		resp, err := c.client.Do(req)
		if err != nil {
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
			_ = c.closeWithError(fmt.Errorf("poll pull request failed: %w", err))
			return
		}
		dialRetry = 0
		backoff = minBackoff

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			_ = c.closeWithError(fmt.Errorf("poll pull bad status: %s", resp.Status))
			return
		}
		c.readiness.markPullReady()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			payload := make([]byte, base64.StdEncoding.DecodedLen(len(line)))
			n, err := base64.StdEncoding.Decode(payload, line)
			if err != nil {
				_ = resp.Body.Close()
				_ = c.closeWithError(fmt.Errorf("poll pull decode failed: %w", err))
				return
			}
			select {
			case c.rxc <- payload[:n]:
			case <-c.closed:
				_ = resp.Body.Close()
				return
			}
		}
		_ = resp.Body.Close()
		if err := scanner.Err(); err != nil {
			_ = c.closeWithError(fmt.Errorf("poll pull scan failed: %w", err))
			return
		}
		if resp.Trailer.Get(tunnelStreamEOFHeader) == "1" {
			c.markReadEOF()
			return
		}
	}
}

func (c *pollConn) pushLoop() {
	const (
		maxBatchBytes   = 64 * 1024
		flushInterval   = 5 * time.Millisecond
		maxLineRawBytes = 16 * 1024
		maxDialRetry    = 12
		minBackoff      = 10 * time.Millisecond
		maxBackoff      = 250 * time.Millisecond
	)

	var (
		buf        bytes.Buffer
		pendingRaw int
		timer      = time.NewTimer(flushInterval)
		writeErr   error
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

		reqCtx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.pushURL, bytes.NewReader(buf.Bytes()))
		if err != nil {
			cancel()
			return err
		}
		req.Host = c.headerHost
		applyTunnelHeaders(req.Header, c.headerHost, TunnelModePoll)
		applyTunnelAuth(req, c.auth, TunnelModePoll, http.MethodPost, "/api/v1/upload")
		req.Header.Set("Content-Type", "text/plain")

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
		pendingRaw = 0
		c.readiness.markPushReady()
		return nil
	}

	flushWithRetry := func() error {
		return retryDial(c.closed, c.closedErr, maxDialRetry, minBackoff, maxBackoff, flush)
	}
	resetTimer(timer, flushInterval)

	enqueue := func(b []byte) error {
		for len(b) > 0 {
			chunk := b
			if len(chunk) > maxLineRawBytes {
				chunk = b[:maxLineRawBytes]
			}
			b = b[len(chunk):]

			encLen := base64.StdEncoding.EncodedLen(len(chunk))
			if pendingRaw+len(chunk) > maxBatchBytes || buf.Len()+encLen+1 > maxBatchBytes*2 {
				if err := flushWithRetry(); err != nil {
					return err
				}
			}

			buf.Grow(encLen + 1)
			tmp := buf.AvailableBuffer()[:encLen]
			base64.StdEncoding.Encode(tmp, chunk)
			tmp = append(tmp, '\n')
			_, _ = buf.Write(tmp)
			pendingRaw += len(chunk)
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
				fail(fmt.Errorf("poll push flush failed: %w", err))
				return
			}

			if pendingRaw >= maxBatchBytes {
				if err := flushWithRetry(); err != nil {
					fail(fmt.Errorf("poll push flush failed: %w", err))
					return
				}
				resetTimer(timer, flushInterval)
			}
		case <-timer.C:
			if err := flushWithRetry(); err != nil {
				fail(fmt.Errorf("poll push flush failed: %w", err))
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
						fail(fmt.Errorf("poll push flush failed: %w", err))
						return
					}
				default:
					if err := flushWithRetry(); err != nil {
						fail(fmt.Errorf("poll push flush failed: %w", err))
						return
					}
					if err := sendSessionControl(c.client, c.finURL, c.headerHost, TunnelModePoll, c.auth); err != nil {
						fail(fmt.Errorf("poll FIN failed: %w", err))
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
