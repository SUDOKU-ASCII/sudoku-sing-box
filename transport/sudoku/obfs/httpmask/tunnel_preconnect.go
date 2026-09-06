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
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/dnsutil"
)

// v0.4.7 servers stop waiting for the first HTTP request after five seconds.
// Rotate raw preconnections before that deadline so a warmed upload never
// hands net/http a socket the peer has already closed.
const preconnectedConnTTL = 4 * time.Second

type preparedConn struct {
	conn      net.Conn
	expiresAt time.Time
}

type preparedConnPool struct {
	mu      sync.Mutex
	ready   []*preparedConn
	pending int
	changed chan struct{}
	refill  chan struct{}
	closed  bool
}

func newPreparedConnPool() *preparedConnPool {
	return &preparedConnPool{
		changed: make(chan struct{}),
		refill:  make(chan struct{}, 1),
	}
}

func (p *preparedConnPool) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *preparedConnPool) requestRefillLocked() {
	select {
	case p.refill <- struct{}{}:
	default:
	}
}

func (p *preparedConnPool) prepare(
	ctx context.Context,
	count int,
	dial func(context.Context) (net.Conn, error),
	done func(),
) {
	p.prepareInternal(ctx, count, false, dial, done)
}

func (p *preparedConnPool) ensure(
	ctx context.Context,
	count int,
	dial func(context.Context) (net.Conn, error),
) {
	p.prepareInternal(ctx, count, true, dial, nil)
}

func (p *preparedConnPool) prepareInternal(
	ctx context.Context,
	count int,
	ensure bool,
	dial func(context.Context) (net.Conn, error),
	done func(),
) {
	if p == nil || count <= 0 || dial == nil {
		if done != nil {
			done()
		}
		return
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		if done != nil {
			done()
		}
		return
	}
	if ensure {
		count -= len(p.ready) + p.pending
		if count <= 0 {
			p.mu.Unlock()
			if done != nil {
				done()
			}
			return
		}
	}
	p.pending += count
	p.notifyLocked()
	p.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(count)
	for range count {
		go func() {
			defer wg.Done()

			conn, err := dial(ctx)

			p.mu.Lock()
			p.pending--
			if err == nil && conn != nil && !p.closed {
				item := &preparedConn{
					conn:      conn,
					expiresAt: time.Now().Add(preconnectedConnTTL),
				}
				p.ready = append(p.ready, item)
				p.notifyLocked()
				p.mu.Unlock()
				go p.expire(item)
				return
			}
			p.notifyLocked()
			p.mu.Unlock()

			if conn != nil {
				_ = conn.Close()
			}
		}()
	}
	if done != nil {
		go func() {
			wg.Wait()
			done()
		}()
	}
}

func (p *preparedConnPool) take(ctx context.Context) (net.Conn, bool, error) {
	if p == nil {
		return nil, false, nil
	}

	for {
		p.mu.Lock()
		if len(p.ready) > 0 {
			item := p.ready[0]
			p.ready[0] = nil
			p.ready = p.ready[1:]
			p.notifyLocked()
			p.requestRefillLocked()
			p.mu.Unlock()
			if item == nil || item.conn == nil {
				continue
			}
			if !item.expiresAt.IsZero() && !time.Now().Before(item.expiresAt) {
				_ = item.conn.Close()
				continue
			}
			return item.conn, true, nil
		}
		if p.pending == 0 || p.closed {
			p.mu.Unlock()
			return nil, false, nil
		}
		changed := p.changed
		p.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

func (p *preparedConnPool) waitReady(ctx context.Context, closed <-chan struct{}, count int) error {
	if p == nil || count <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		select {
		case <-closed:
			return net.ErrClosed
		default:
		}

		p.mu.Lock()
		if len(p.ready) >= count {
			p.mu.Unlock()
			return nil
		}
		if p.closed {
			p.mu.Unlock()
			return net.ErrClosed
		}
		changed := p.changed
		p.mu.Unlock()

		select {
		case <-changed:
		case <-closed:
			return net.ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (p *preparedConnPool) expire(item *preparedConn) {
	delay := time.Until(item.expiresAt)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C

	p.mu.Lock()
	for i, candidate := range p.ready {
		if candidate != item {
			continue
		}
		copy(p.ready[i:], p.ready[i+1:])
		p.ready[len(p.ready)-1] = nil
		p.ready = p.ready[:len(p.ready)-1]
		p.notifyLocked()
		p.requestRefillLocked()
		p.mu.Unlock()
		_ = item.conn.Close()
		return
	}
	p.mu.Unlock()
}

func (p *preparedConnPool) close() {
	if p == nil {
		return
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	ready := p.ready
	p.ready = nil
	p.notifyLocked()
	p.mu.Unlock()

	for _, item := range ready {
		if item != nil && item.conn != nil {
			_ = item.conn.Close()
		}
	}
}

type preconnectDialer struct {
	urlHost     string
	dialAddr    string
	serverName  string
	tlsConfig   *tls.Config
	dialContext func(context.Context, string, string) (net.Conn, error)
	pool        *preparedConnPool
}

func newPreconnectDialer(urlHost, dialAddr, serverName string, tlsConfig *tls.Config, dialContext func(context.Context, string, string) (net.Conn, error)) *preconnectDialer {
	return &preconnectDialer{
		urlHost:     urlHost,
		dialAddr:    dialAddr,
		serverName:  serverName,
		tlsConfig:   tlsConfig,
		dialContext: dialContext,
		pool:        newPreparedConnPool(),
	}
}

func (d *preconnectDialer) preconnect(ctx context.Context, tlsEnabled bool, count int) context.CancelFunc {
	if d == nil || d.pool == nil {
		return func() {}
	}

	// Auto cancels its stream probe after a successful dial, so retain only its
	// absolute deadline while the initial pull and push connections finish.
	deadline := time.Now().Add(preconnectedConnTTL)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	dialCtx, cancel := context.WithDeadline(context.Background(), deadline)
	d.pool.prepare(dialCtx, count, func(dialCtx context.Context) (net.Conn, error) {
		if tlsEnabled {
			return d.dialTLSFresh(dialCtx, "tcp", d.urlHost)
		}
		return d.dialFresh(dialCtx, "tcp", d.urlHost)
	}, cancel)
	return cancel
}

func (d *preconnectDialer) maintainPreconnect(ctx context.Context, tlsEnabled bool, count int) {
	if d == nil || d.pool == nil || count <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	const retryInterval = 500 * time.Millisecond
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	ensure := func() {
		d.pool.ensure(ctx, count, func(ctx context.Context) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, tunnelTLSHandshakeTimeout)
			defer cancel()
			if tlsEnabled {
				return d.dialTLSFresh(dialCtx, "tcp", d.urlHost)
			}
			return d.dialFresh(dialCtx, "tcp", d.urlHost)
		})
	}

	ensure()
	for {
		select {
		case <-ticker.C:
		case <-d.pool.refill:
		case <-ctx.Done():
			return
		}
		ensure()
	}
}

func (d *preconnectDialer) dialContextFromPool(ctx context.Context, network, addr string) (net.Conn, error) {
	if d != nil && addr == d.urlHost {
		if conn, ok, err := d.pool.take(ctx); err != nil || ok {
			return conn, err
		}
	}
	return d.dialFresh(ctx, network, addr)
}

func (d *preconnectDialer) dialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d != nil && addr == d.urlHost {
		if conn, ok, err := d.pool.take(ctx); err != nil || ok {
			return conn, err
		}
	}
	return d.dialTLSFresh(ctx, network, addr)
}

func (d *preconnectDialer) dialFresh(ctx context.Context, network, addr string) (net.Conn, error) {
	if d != nil && addr == d.urlHost {
		addr = d.dialAddr
	}
	if d != nil && d.dialContext != nil {
		return d.dialContext(ctx, network, addr)
	}
	return dnsutil.OutboundDialer(0).DialContext(ctx, network, addr)
}

func (d *preconnectDialer) dialTLSFresh(ctx context.Context, network, addr string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, tunnelTLSHandshakeTimeout)
	defer cancel()

	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if d != nil && d.tlsConfig != nil {
		config = d.tlsConfig.Clone()
	}
	if d != nil && addr == d.urlHost {
		config.ServerName = d.serverName
		addr = d.dialAddr
	} else {
		config.ServerName = trimPortForHost(addr)
	}
	rawConn, err := d.dialFresh(dialCtx, network, addr)
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(rawConn, config)
	if err = tlsConn.HandshakeContext(dialCtx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (d *preconnectDialer) close() {
	if d != nil {
		d.pool.close()
	}
}
