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
package dnsutil

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	dohPort                = "443"
	dohPath                = "/dns-query"
	dohHandshakeTimeoutMax = 10 * time.Second
	maxDoHResponseBytes    = 64 << 10 // the DNS message size limit
)

type httpsNameServer struct {
	serverURL string
	hostPort  string
	bootstrap []string // "ip:443", dialed round-robin instead of resolving the server name
	next      atomic.Uint32
	client    *http.Client
	local     nameServer // answers intranet-looking names the public server cannot know
}

func newHTTPSNameServer(srv ServerOptions, timeout time.Duration) (*httpsNameServer, error) {
	host := strings.TrimSpace(srv.Address)
	if host == "" {
		return nil, errors.New("https dns server requires address")
	}
	ns := &httpsNameServer{
		serverURL: "https://" + net.JoinHostPort(host, dohPort) + dohPath,
		hostPort:  net.JoinHostPort(host, dohPort),
		local:     localNameServer{lookup: systemLookup},
	}
	for _, addr := range srv.Bootstrap {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := ParseIPLiteral(addr); !ok {
			return nil, fmt.Errorf("invalid bootstrap ip %q", addr)
		}
		ns.bootstrap = append(ns.bootstrap, net.JoinHostPort(addr, dohPort))
	}
	ns.client = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			ForceAttemptHTTP2:     true,
			DisableCompression:    true,
			ResponseHeaderTimeout: timeout,
			TLSHandshakeTimeout:   min(timeout, dohHandshakeTimeoutMax),
			TLSClientConfig:       &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
			DialContext:           ns.dialContext,
		},
	}
	return ns, nil
}

func (s *httpsNameServer) dialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	target := s.hostPort
	if len(s.bootstrap) > 0 {
		target = s.bootstrap[int(s.next.Add(1)-1)%len(s.bootstrap)]
	}
	return OutboundDialer(0).DialContext(ctx, network, target)
}

func (s *httpsNameServer) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	if isLikelyLocalHostname(host) {
		return s.local.LookupIPv4(ctx, host)
	}
	query, err := buildDNSQuery(host)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.serverURL, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("Content-Type", "application/dns-message")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("doh http %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDoHResponseBytes))
	if err != nil {
		return nil, err
	}
	return parseDNSAnswerIPv4s(body)
}

func buildDNSQuery(host string) ([]byte, error) {
	name, err := dnsmessage.NewName(strings.TrimSuffix(strings.TrimSpace(host), ".") + ".")
	if err != nil {
		return nil, err
	}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{RecursionDesired: true})
	builder.EnableCompression()
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return builder.Finish()
}

func parseDNSAnswerIPv4s(resp []byte) ([]netip.Addr, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(resp)
	if err != nil {
		return nil, err
	}
	if header.RCode != dnsmessage.RCodeSuccess {
		return nil, fmt.Errorf("dns rcode=%s", header.RCode)
	}
	if err := parser.SkipAllQuestions(); err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	for {
		h, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return addrs, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Type != dnsmessage.TypeA {
			if err := parser.SkipAnswer(); err != nil {
				return nil, err
			}
			continue
		}
		body, err := parser.AResource()
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, netip.AddrFrom4(body.A))
	}
}
