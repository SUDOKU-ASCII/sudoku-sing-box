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
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	splitRequestTimeout = 20 * time.Second
	splitFlushInterval  = 5 * time.Millisecond
	splitMinBackoff     = 10 * time.Millisecond
	splitMaxBackoff     = 250 * time.Millisecond

	streamMaxBatchBytes   = 256 * 1024
	streamPullIdleBackoff = 25 * time.Millisecond

	pollMaxBatchBytes = 64 * 1024
	pollMaxLineBytes  = 16 * 1024
)

var errSplitClosed = errors.New("split tunnel closed")

// splitCodec captures what differs between stream (raw octet bodies) and poll (base64 text lines).
type splitCodec struct {
	mode              TunnelMode
	uploadContentType string
	maxBatchBytes     int           // raw payload bytes per upload request
	maxLineBytes      int           // raw payload bytes per encoded upload line
	pullIdleBackoff   time.Duration // pause before re-pulling after a response that carried no data

	encodedLen    func(rawLen int) int
	appendEncoded func(dst, raw []byte) []byte
	// readPull hands each decoded downlink payload to deliver until the body ends; a false deliver aborts with errSplitClosed.
	readPull func(body io.Reader, deliver func([]byte) bool) (readAny bool, err error)
}

var splitStreamCodec = splitCodec{
	mode:              TunnelModeStream,
	uploadContentType: "application/octet-stream",
	maxBatchBytes:     streamMaxBatchBytes,
	maxLineBytes:      math.MaxInt,
	pullIdleBackoff:   streamPullIdleBackoff,
	encodedLen:        func(rawLen int) int { return rawLen },
	appendEncoded:     func(dst, raw []byte) []byte { return append(dst, raw...) },
	readPull:          readStreamPull,
}

var splitPollCodec = splitCodec{
	mode:              TunnelModePoll,
	uploadContentType: "text/plain",
	maxBatchBytes:     pollMaxBatchBytes,
	maxLineBytes:      pollMaxLineBytes,
	encodedLen:        func(rawLen int) int { return base64.StdEncoding.EncodedLen(rawLen) + 1 },
	appendEncoded:     appendBase64Line,
	readPull:          readPollPull,
}

func appendBase64Line(dst, raw []byte) []byte {
	start := len(dst)
	dst = append(dst, make([]byte, base64.StdEncoding.EncodedLen(len(raw)))...)
	base64.StdEncoding.Encode(dst[start:], raw)
	return append(dst, '\n')
}

func readStreamPull(body io.Reader, deliver func([]byte) bool) (readAny bool, err error) {
	buf := getPullPayload()
	defer func() { putPullPayload(buf) }()
	for {
		n, rerr := body.Read(buf[:])
		if n > 0 {
			readAny = true
			var payload []byte
			if n >= pullHandoverMinBytes {
				// Mostly full: the reader takes the buffer itself, which saves a copy; chanConn returns it to the pool.
				payload = buf[:n]
				buf = getPullPayload()
			} else {
				payload = append([]byte(nil), buf[:n]...)
			}
			if !deliver(payload) {
				return readAny, errSplitClosed
			}
		}
		if errors.Is(rerr, io.EOF) {
			return readAny, nil
		}
		if rerr != nil {
			return readAny, rerr
		}
	}
}

func readPollPull(body io.Reader, deliver func([]byte) bool) (readAny bool, err error) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		payload := make([]byte, base64.StdEncoding.DecodedLen(len(line)))
		n, derr := base64.StdEncoding.Decode(payload, line)
		if derr != nil {
			return readAny, fmt.Errorf("decode: %w", derr)
		}
		readAny = true
		if !deliver(payload[:n]) {
			return readAny, errSplitClosed
		}
	}
	return readAny, scanner.Err()
}

// splitConn is the client end of a split session: uploads leave as POST batches, the downlink arrives on a
// long-running GET, and chanConn bridges both to a net.Conn.
type splitConn struct {
	chanConn
	codec     *splitCodec
	readiness *tunnelReadiness

	ctx    context.Context
	cancel context.CancelFunc

	client     *http.Client
	pushURL    string
	pullURL    string
	finURL     string
	closeURL   string
	headerHost string
	uploadSeq  atomic.Uint64
}

func dialStream(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	return dialSplit(ctx, serverAddress, opts, &splitStreamCodec)
}

func dialPoll(ctx context.Context, serverAddress string, opts TunnelDialOptions) (net.Conn, error) {
	return dialSplit(ctx, serverAddress, opts, &splitPollCodec)
}

func dialSplit(ctx context.Context, serverAddress string, opts TunnelDialOptions, codec *splitCodec) (net.Conn, error) {
	info, err := dialSession(ctx, serverAddress, opts, codec.mode)
	if err != nil {
		return nil, err
	}

	connCtx, cancel := context.WithCancel(context.Background())
	c := &splitConn{
		chanConn:   newChanConn(),
		codec:      codec,
		readiness:  newTunnelReadiness(),
		ctx:        connCtx,
		cancel:     cancel,
		client:     info.client,
		pushURL:    info.pushURL,
		pullURL:    info.pullURL,
		finURL:     info.finURL,
		closeURL:   info.closeURL,
		headerHost: info.headerHost,
	}

	go c.pullLoop()
	go c.pushLoop()
	conn, err := upgradeDialedConn(c, opts)
	if err != nil {
		return nil, err
	}
	return wrapReadyTunnelConn(conn, c.waitReady), nil
}

func (c *splitConn) Close() error {
	return c.closeWithError(io.ErrClosedPipe)
}

func (c *splitConn) closeWithError(err error) error {
	_ = c.chanConn.closeWithError(err)
	c.cancel()
	_ = sendSessionControl(c.client, c.closeURL, c.headerHost, c.codec.mode)
	return nil
}

func (c *splitConn) waitReady(ctx context.Context) error {
	return c.readiness.wait(ctx, c.closed, c.closedErr)
}

func (c *splitConn) deliver(payload []byte) bool {
	select {
	case c.rxc <- payload:
		return true
	case <-c.closed:
		return false
	}
}

type pullOutcome int

const (
	pullAgain pullOutcome = iota // the long-poll ended healthily
	pullRetry                    // transient failure: back off, then pull again
	pullStop                     // conn closed or downlink finished
)

func (c *splitConn) pullLoop() {
	backoff := splitMinBackoff
	for {
		select {
		case <-c.closed:
			return
		default:
		}

		outcome, readAny := c.pullOnce()
		switch outcome {
		case pullStop:
			return
		case pullRetry:
			if err := waitRetry(c.closed, c.closedErr, backoff); err != nil {
				return
			}
			backoff = nextBackoff(backoff, splitMinBackoff, splitMaxBackoff)
		case pullAgain:
			backoff = splitMinBackoff
			if !readAny && c.codec.pullIdleBackoff > 0 {
				// Avoid a tight loop when the server answered immediately with an empty body.
				select {
				case <-time.After(c.codec.pullIdleBackoff):
				case <-c.closed:
					return
				}
			}
		}
	}
}

// pullOnce runs one downlink GET. Fatal failures close the conn before returning pullStop.
func (c *splitConn) pullOnce() (outcome pullOutcome, readAny bool) {
	mode := c.codec.mode
	// The server ends an idle pull after PullReadTimeout; a fixed client lifetime would
	// instead cancel healthy continuous downloads mid-stream.
	reqCtx, cancel := context.WithCancel(c.ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.pullURL, nil)
	if err != nil {
		_ = c.closeWithError(err)
		return pullStop, false
	}
	req.Host = c.headerHost
	applyTunnelHeaders(req.Header, c.headerHost, mode)

	resp, err := c.client.Do(req)
	if err != nil {
		if isDialError(err) || isRetryableHTTPTransportError(err) {
			closeIdleConnections(c.client)
			return pullRetry, false
		}
		_ = c.closeWithError(fmt.Errorf("%s pull request failed: %w", mode, err))
		return pullStop, false
	}

	if resp.StatusCode != http.StatusOK {
		if isRetryableStatusCode(resp.StatusCode) {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
			_ = resp.Body.Close()
			closeIdleConnections(c.client)
			return pullRetry, false
		}
		_ = resp.Body.Close()
		_ = c.closeWithError(fmt.Errorf("%s pull bad status: %s", mode, resp.Status))
		return pullStop, false
	}
	c.readiness.markPullReady()

	readAny, err = c.codec.readPull(resp.Body, c.deliver)
	_ = resp.Body.Close()
	switch {
	case errors.Is(err, errSplitClosed):
		return pullStop, readAny
	case err == nil:
		if resp.Trailer.Get(tunnelStreamEOFHeader) == "1" {
			c.markReadEOF()
			return pullStop, readAny
		}
		return pullAgain, readAny
	case isRetryableHTTPTransportError(err):
		closeIdleConnections(c.client)
		return pullRetry, readAny
	default:
		_ = c.closeWithError(fmt.Errorf("%s pull failed: %w", mode, err))
		return pullStop, readAny
	}
}

// postUpload sends one encoded batch; the caller retries it until it succeeds or the conn closes.
func (c *splitConn) postUpload(requestURL string, payload []byte) error {
	reqCtx, cancel := context.WithTimeout(c.ctx, splitRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Host = c.headerHost
	applyTunnelHeaders(req.Header, c.headerHost, c.codec.mode)
	req.Header.Set("Content-Type", c.codec.uploadContentType)

	resp, err := c.client.Do(req)
	if err != nil {
		closeIdleConnections(c.client)
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err = statusError(resp)
		if isRetryableHTTPTransportError(err) {
			closeIdleConnections(c.client)
		}
		return err
	}
	return nil
}

func (c *splitConn) pushLoop() {
	codec := c.codec
	var (
		buf        []byte
		pendingRaw int
		timer      = time.NewTimer(splitFlushInterval)
		timerArmed bool
		writeErr   error
	)
	// The timer runs only while a batch is buffered, so idle sessions never wake up.
	timer.Stop()
	defer timer.Stop()
	defer func() { c.completeWrite(writeErr) }()

	fail := func(err error) {
		writeErr = err
		_ = c.closeWithError(err)
	}
	failFlush := func(err error) { fail(fmt.Errorf("%s push flush failed: %w", codec.mode, err)) }

	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		requestURL := uploadURL(c.pushURL, c.uploadSeq.Add(1))
		attempts := 0
		if err := retryPersistent(c.closed, c.closedErr, splitMinBackoff, splitMaxBackoff, func() error {
			attempts++
			return c.postUpload(requestURL, buf)
		}); err != nil {
			return err
		}
		if attempts == 1 {
			buf = buf[:0]
		} else {
			// A failed attempt's transport goroutine may still be reading the old batch.
			buf = nil
		}
		pendingRaw = 0
		timer.Stop()
		timerArmed = false
		c.readiness.markPushReady()
		return nil
	}

	enqueue := func(b []byte) error {
		for len(b) > 0 {
			line := b[:min(len(b), codec.maxLineBytes)]
			b = b[len(line):]
			if pendingRaw+len(line) > codec.maxBatchBytes || len(buf)+codec.encodedLen(len(line)) > 2*codec.maxBatchBytes {
				if err := flush(); err != nil {
					return err
				}
			}
			buf = codec.appendEncoded(buf, line)
			pendingRaw += len(line)
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
				failFlush(err)
				return
			}
			if pendingRaw >= codec.maxBatchBytes {
				if err := flush(); err != nil {
					failFlush(err)
					return
				}
			} else if !timerArmed {
				timer.Reset(splitFlushInterval)
				timerArmed = true
			}
		case <-timer.C:
			timerArmed = false
			if err := flush(); err != nil {
				failFlush(err)
				return
			}
		case <-c.writeClosed:
			// Drain any already-accepted writes so CloseWrite does not lose data.
			for drained := false; !drained; {
				select {
				case b := <-c.writeCh:
					if err := enqueue(b); err != nil {
						failFlush(err)
						return
					}
				default:
					drained = true
				}
			}
			if err := flush(); err != nil {
				failFlush(err)
				return
			}
			if err := sendSessionControl(c.client, c.finURL, c.headerHost, codec.mode); err != nil {
				fail(fmt.Errorf("%s FIN failed: %w", codec.mode, err))
			}
			return
		case <-c.closed:
			writeErr = c.closedErr()
			return
		}
	}
}
