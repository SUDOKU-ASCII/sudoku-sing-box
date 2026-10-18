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
	"container/list"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

// Enough sockets to overlap authorize, pull and the first upload; a fourth
// "spare" would add a WAN RTT before mux can open its first stream.
const (
	tunnelPreconnectCount     = 3
	tunnelTLSHandshakeTimeout = 10 * time.Second
)

type transportKey struct {
	scheme     string
	urlHost    string
	dialAddr   string
	serverName string
}

type transportCacheEntry struct {
	key       transportKey
	transport *tunnelHTTPTransport
}

type tunnelHTTPTransport struct {
	transport *http.Transport
	dialer    *preconnectDialer
}

func (t *tunnelHTTPTransport) closeIdleConnections() {
	if t != nil && t.transport != nil {
		t.transport.CloseIdleConnections()
	}
}

func (t *tunnelHTTPTransport) close() {
	if t == nil {
		return
	}
	if t.transport != nil {
		t.transport.CloseIdleConnections()
	}
	if t.dialer != nil {
		t.dialer.close()
	}
}

type tunnelHTTPClient struct {
	client    *http.Client
	transport *tunnelHTTPTransport
}

func (c *tunnelHTTPClient) preconnect(ctx context.Context, req *http.Request, count int) context.CancelFunc {
	if c == nil || c.transport == nil || c.transport.transport == nil || c.transport.dialer == nil ||
		req == nil || req.URL == nil || count <= 0 {
		return func() {}
	}

	if proxy := c.transport.transport.Proxy; proxy != nil {
		proxyURL, err := proxy(req)
		if err != nil || proxyURL != nil {
			return func() {}
		}
	}
	return c.transport.dialer.preconnect(ctx, req.URL.Scheme == "https", count)
}

// transportCache bounds the memory footprint of globally reused HTTP transports and dialers.
//
// Each dial creates its own http.Client, but clients can share Transports to reuse
// underlying TCP/TLS connections (keep-alive / HTTP/2) when Multiplex is enabled.
type transportCache struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	m   map[transportKey]*list.Element
}

func newTransportCache(maxEntries int) *transportCache {
	if maxEntries <= 0 {
		maxEntries = 64
	}
	return &transportCache{
		max: maxEntries,
		ll:  list.New(),
		m:   make(map[transportKey]*list.Element),
	}
}

func (c *transportCache) getOrCreate(
	key transportKey,
	build func() *tunnelHTTPTransport,
) *tunnelHTTPTransport {
	if c == nil {
		return build()
	}

	c.mu.Lock()
	if el := c.m[key]; el != nil {
		c.ll.MoveToFront(el)
		ent := el.Value.(*transportCacheEntry)
		transport := ent.transport
		c.mu.Unlock()
		return transport
	}
	c.mu.Unlock()

	transport := build()

	c.mu.Lock()
	// Another goroutine might have inserted while we were building.
	if el := c.m[key]; el != nil {
		c.ll.MoveToFront(el)
		ent := el.Value.(*transportCacheEntry)
		existing := ent.transport
		c.mu.Unlock()
		transport.close()
		return existing
	}
	el := c.ll.PushFront(&transportCacheEntry{key: key, transport: transport})
	c.m[key] = el
	for c.max > 0 && c.ll.Len() > c.max {
		back := c.ll.Back()
		if back == nil {
			break
		}
		ent := back.Value.(*transportCacheEntry)
		delete(c.m, ent.key)
		c.ll.Remove(back)
		ent.transport.close()
	}
	c.mu.Unlock()
	return transport
}

var globalTransportCache = newTransportCache(128)

// TransportPool owns a bounded set of reusable HTTP transports. Embedders use
// it to keep connection reuse isolated from unrelated Sudoku clients.
type TransportPool struct {
	once  sync.Once
	cache *transportCache
}

func (p *TransportPool) cacheOrNew() *transportCache {
	if p == nil {
		return newTransportCache(64)
	}
	p.once.Do(func() {
		p.cache = newTransportCache(64)
	})
	return p.cache
}

func (p *TransportPool) getOrCreateTransport(key transportKey, build func() *tunnelHTTPTransport) *tunnelHTTPTransport {
	return p.cacheOrNew().getOrCreate(key, build)
}

func (p *TransportPool) CloseIdleConnections() {
	if p == nil {
		return
	}
	cache := p.cacheOrNew()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for el := cache.ll.Front(); el != nil; el = el.Next() {
		if ent, ok := el.Value.(*transportCacheEntry); ok {
			ent.transport.closeIdleConnections()
		}
	}
}

func (p *TransportPool) Close() error {
	if p == nil {
		return nil
	}
	cache := p.cacheOrNew()
	cache.mu.Lock()
	entries := make([]*tunnelHTTPTransport, 0, cache.ll.Len())
	for el := cache.ll.Front(); el != nil; el = el.Next() {
		if ent, ok := el.Value.(*transportCacheEntry); ok && ent.transport != nil {
			entries = append(entries, ent.transport)
		}
	}
	cache.ll.Init()
	clear(cache.m)
	cache.mu.Unlock()
	for _, transport := range entries {
		transport.close()
	}
	return nil
}

func newHTTPClient(
	urlHost, dialAddr, serverName, scheme string,
	maxIdleConns int,
	reuseTransport bool,
	pool *TransportPool,
	dialContext func(context.Context, string, string) (net.Conn, error),
) *tunnelHTTPClient {
	build := func() *tunnelHTTPTransport {
		var transportTLSConfig, dialerTLSConfig *tls.Config
		if scheme == "https" {
			transportTLSConfig = &tls.Config{
				ServerName: serverName,
				MinVersion: tls.VersionTLS12,
				NextProtos: []string{"h2", "http/1.1"},
			}
			dialerTLSConfig = transportTLSConfig.Clone()
		}
		dialer := newPreconnectDialer(urlHost, dialAddr, serverName, dialerTLSConfig, dialContext)
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     scheme == "https",
			DisableCompression:    true,
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConns,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			TLSHandshakeTimeout:   tunnelTLSHandshakeTimeout,
			DialContext:           dialer.dialContext,
		}
		if scheme == "https" {
			transport.TLSClientConfig = transportTLSConfig
			transport.DialTLSContext = dialer.dialTLSContext
		}
		return &tunnelHTTPTransport{transport: transport, dialer: dialer}
	}

	var transport *tunnelHTTPTransport
	if !reuseTransport {
		transport = build()
	} else {
		key := transportKey{
			scheme:     scheme,
			urlHost:    urlHost,
			dialAddr:   dialAddr,
			serverName: serverName,
		}
		switch {
		case pool != nil:
			transport = pool.getOrCreateTransport(key, build)
		case dialContext != nil:
			// A function-valued dialer cannot be represented safely in transportKey.
			transport = build()
		default:
			transport = globalTransportCache.getOrCreate(key, build)
		}
	}

	return &tunnelHTTPClient{
		client:    &http.Client{Transport: transport.transport},
		transport: transport,
	}
}
