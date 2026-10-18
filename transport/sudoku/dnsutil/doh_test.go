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
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// dohTestAnswers builds a DNS response to query carrying the given records.
func dohTestAnswers(t *testing.T, query []byte, rcode dnsmessage.RCode, records ...dnsmessage.Resource) []byte {
	t.Helper()
	var parser dnsmessage.Parser
	header, err := parser.Start(query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	q, err := parser.Question()
	if err != nil {
		t.Fatalf("parse question: %v", err)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, RecursionDesired: true, RecursionAvailable: true, RCode: rcode})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(q); err != nil {
		t.Fatal(err)
	}
	if err := b.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		rec.Header.Name = q.Name
		rec.Header.Class = dnsmessage.ClassINET
		rec.Header.TTL = 60
		var err error
		switch body := rec.Body.(type) {
		case *dnsmessage.AResource:
			rec.Header.Type = dnsmessage.TypeA
			err = b.AResource(rec.Header, *body)
		case *dnsmessage.AAAAResource:
			rec.Header.Type = dnsmessage.TypeAAAA
			err = b.AAAAResource(rec.Header, *body)
		default:
			t.Fatalf("unsupported record body %T", body)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func aRecord(a, b, c, d byte) dnsmessage.Resource {
	return dnsmessage.Resource{Body: &dnsmessage.AResource{A: [4]byte{a, b, c, d}}}
}

// newTestDoHServer starts a TLS DoH endpoint answering every query with handle and
// returns a name server whose transport is pointed at it.
func newTestDoHServer(t *testing.T, handle func(t *testing.T, query []byte) (status int, body []byte)) (*httpsNameServer, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/dns-query" ||
			r.Header.Get("Content-Type") != "application/dns-message" ||
			r.Header.Get("Accept") != "application/dns-message" {
			t.Errorf("unexpected request %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		query, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read query: %v", err)
		}
		status, body := handle(t, query)
		w.Header().Set("Content-Type", "application/dns-message")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	ns, err := newHTTPSNameServer(ServerOptions{Type: serverTypeDoH, Address: "example.com", Bootstrap: []string{"127.0.0.1"}}, 2*time.Second)
	if err != nil {
		t.Fatalf("newHTTPSNameServer: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr := ns.client.Transport.(*http.Transport)
	tr.TLSClientConfig = &tls.Config{ServerName: "example.com", RootCAs: pool, MinVersion: tls.VersionTLS12}
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	return ns, &requests
}

func TestHTTPSNameServerLookupIP(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		rcode   dnsmessage.RCode
		records []dnsmessage.Resource
		want    []string
		wantErr string
	}{
		{name: "a records", status: 200, records: []dnsmessage.Resource{aRecord(1, 2, 3, 4), aRecord(5, 6, 7, 8)}, want: []string{"1.2.3.4", "5.6.7.8"}},
		{name: "aaaa answers are ignored", status: 200, records: []dnsmessage.Resource{
			{Body: &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 0x0d, 0xb8, 15: 1}}}, aRecord(1, 2, 3, 4),
		}, want: []string{"1.2.3.4"}},
		{name: "no answers", status: 200, want: nil},
		{name: "non-success rcode", status: 200, rcode: dnsmessage.RCodeNameError, wantErr: "dns rcode"},
		{name: "http error", status: 503, wantErr: "doh http 503"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns, _ := newTestDoHServer(t, func(t *testing.T, query []byte) (int, []byte) {
				if tc.status != 200 {
					return tc.status, []byte("nope")
				}
				return tc.status, dohTestAnswers(t, query, tc.rcode, tc.records...)
			})
			ips, err := ns.LookupIPv4(context.Background(), "Lookup.Test")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LookupIP: %v", err)
			}
			var got []string
			for _, ip := range ips {
				got = append(got, ip.String())
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("ips = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHTTPSNameServerQueryShape(t *testing.T) {
	var gotName atomic.Value
	ns, requests := newTestDoHServer(t, func(t *testing.T, query []byte) (int, []byte) {
		var p dnsmessage.Parser
		h, err := p.Start(query)
		if err != nil {
			t.Errorf("parse: %v", err)
		}
		q, _ := p.Question()
		if q.Type != dnsmessage.TypeA || q.Class != dnsmessage.ClassINET || !h.RecursionDesired || h.Response {
			t.Errorf("unexpected question %+v header %+v", q, h)
		}
		gotName.Store(q.Name.String())
		return 200, dohTestAnswers(t, query, dnsmessage.RCodeSuccess, aRecord(9, 9, 9, 9))
	})
	if _, err := ns.LookupIPv4(context.Background(), "www.Example.org."); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || gotName.Load() != "www.Example.org." {
		t.Fatalf("requests=%d name=%v", requests.Load(), gotName.Load())
	}
}

func TestHTTPSNameServerLocalNamesBypassDoH(t *testing.T) {
	ns, requests := newTestDoHServer(t, func(t *testing.T, query []byte) (int, []byte) {
		return 200, dohTestAnswers(t, query, dnsmessage.RCodeSuccess)
	})
	ns.local = localNameServer{lookup: func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.9.8.7")}, nil
	}}
	for _, host := range []string{"printer", "nas.local", "router.lan"} {
		ips, err := ns.LookupIPv4(context.Background(), host)
		if err != nil || len(ips) != 1 || ips[0].String() != "10.9.8.7" {
			t.Fatalf("%s: ips=%v err=%v, want the local fallback", host, ips, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("local-looking names must not reach the DoH server, got %d requests", requests.Load())
	}
}
