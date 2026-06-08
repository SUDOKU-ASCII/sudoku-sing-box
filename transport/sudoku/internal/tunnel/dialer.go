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
package tunnel

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/dnsutil"
	"github.com/sagernet/sing-box/transport/sudoku/internal/config"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

// Dialer abstracts the logic for establishing a connection to the server.
type Dialer interface {
	Dial(destAddrStr string) (net.Conn, error)
}

// BaseDialer contains common logic for Sudoku connections.
type BaseDialer struct {
	Config        *config.Config
	Tables        []*sudoku.Table
	PrivateKey    []byte
	Context       context.Context
	DialContext   func(ctx context.Context, network, addr string) (net.Conn, error)
	TransportPool *httpmask.TransportPool
}

// DialBase establishes a Sudoku tunnel connection to the configured server,
// performing the handshake but not requesting any target address.
func (d *BaseDialer) DialBase() (net.Conn, error) {
	return d.dialBaseWithUplinkMode(ObfsUplinkPure)
}

// DialReverseBase establishes a reverse-session tunnel using packed uplink on the client side.
func (d *BaseDialer) DialReverseBase() (net.Conn, error) {
	return d.dialBaseWithUplinkMode(ObfsUplinkPacked)
}

func (d *BaseDialer) dialBase() (net.Conn, error) {
	return d.dialBaseWithUplinkMode(ObfsUplinkPure)
}

func (d *BaseDialer) dialHTTPMaskTunnel(dialCtx context.Context, table *sudoku.Table, tableHint uint32, hasTableHint bool, uplinkMode ObfsUplinkMode, upgrade func(net.Conn) (net.Conn, error)) (net.Conn, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("missing config")
	}
	earlyHandshake, err := NewHTTPMaskClientEarlyHandshake(EarlyCodecConfig{
		PSK:                d.Config.Key,
		AEAD:               d.Config.AEAD,
		EnablePureDownlink: d.Config.EnablePureDownlink,
		PaddingMin:         d.Config.PaddingMin,
		PaddingMax:         d.Config.PaddingMax,
		PackedUplink:       uplinkMode == ObfsUplinkPacked,
	}, table, tableHint, hasTableHint, kipUserHashFromPrivateKey(d.PrivateKey, d.Config.Key), KIPFeatAll)
	if err != nil {
		return nil, err
	}
	opts := httpmask.TunnelDialOptions{
		Mode:           d.Config.HTTPMask.Mode,
		TLSEnabled:     d.Config.HTTPMask.TLS,
		HostOverride:   d.Config.HTTPMask.Host,
		PathRoot:       d.Config.HTTPMask.PathRoot,
		AuthKey:        d.Config.Key,
		EarlyHandshake: earlyHandshake,
		Upgrade: func(raw net.Conn) (net.Conn, error) {
			return upgrade(raw)
		},
		Multiplex:     d.Config.MultiplexMode(),
		TransportPool: d.TransportPool,
		DialContext:   d.DialContext,
	}
	return httpmask.DialTunnel(dialCtx, d.Config.ServerAddress, opts)
}

func (d *BaseDialer) pickTable() (*sudoku.Table, uint32, bool, error) {
	if len(d.Tables) == 0 {
		return nil, 0, false, fmt.Errorf("no table configured")
	}
	if len(d.Tables) == 1 {
		return d.Tables[0], d.Tables[0].Hint(), false, nil
	}
	// Use crypto/rand to avoid shared global RNG in concurrent dialing.
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, 0, false, fmt.Errorf("random table pick failed: %w", err)
	}
	idx := int(b[0]) % len(d.Tables)
	return d.Tables[idx], d.Tables[idx].Hint(), true, nil
}

func (d *BaseDialer) dialBaseWithUplinkMode(uplinkMode ObfsUplinkMode) (net.Conn, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("missing config")
	}

	var baseConn net.Conn

	// HTTP tunnel (CDN-friendly) modes. The returned conn already strips HTTP headers.
	if d.Config.HTTPMaskTunnelEnabled() {
		baseCtx := d.Context
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		dialCtx, cancel := context.WithTimeout(baseCtx, 10*time.Second)
		defer cancel()
		table, tableHint, hasTableHint, err := d.pickTable()
		if err != nil {
			return nil, err
		}
		conn, err := d.dialHTTPMaskTunnel(dialCtx, table, tableHint, hasTableHint, uplinkMode, func(raw net.Conn) (net.Conn, error) {
			return ClientHandshakeWithUplinkMode(raw, d.Config, table, d.PrivateKey, uplinkMode, tableHint, hasTableHint)
		})
		if err != nil {
			return nil, fmt.Errorf("dial http tunnel failed: %w", err)
		}
		baseConn = conn
	} else {
		var (
			rawRemote net.Conn
			err       error
		)
		baseCtx := d.Context
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		if d.DialContext != nil {
			dialCtx, cancel := context.WithTimeout(baseCtx, 10*time.Second)
			defer cancel()
			rawRemote, err = d.DialContext(dialCtx, "tcp", d.Config.ServerAddress)
		} else {
			// Resolve server address with DNS concurrency and optimistic cache.
			resolveCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
			defer cancel()

			serverAddr, resolveErr := dnsutil.ResolveWithCache(resolveCtx, d.Config.ServerAddress)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve server address failed: %w", resolveErr)
			}

			rawRemote, err = dnsutil.OutboundDialer(5*time.Second).Dial("tcp", serverAddr)
		}
		if err != nil {
			return nil, fmt.Errorf("dial server failed: %w", err)
		}

		// 2. Send HTTP mask
		if !d.Config.HTTPMask.Disable {
			// Legacy HTTP mask (not CDN-compatible): write a fake HTTP/1.1 header then switch to raw stream.
			if err := httpmask.WriteRandomRequestHeaderWithPathRoot(rawRemote, d.Config.ServerAddress, d.Config.HTTPMask.PathRoot); err != nil {
				rawRemote.Close()
				return nil, fmt.Errorf("write http mask failed: %w", err)
			}
		}

		table, tableHint, hasTableHint, err := d.pickTable()
		if err != nil {
			rawRemote.Close()
			return nil, err
		}
		baseConn, err = ClientHandshakeWithUplinkMode(rawRemote, d.Config, table, d.PrivateKey, uplinkMode, tableHint, hasTableHint)
		if err != nil {
			return nil, err
		}
	}

	return baseConn, nil
}

func (d *BaseDialer) dialTarget(destAddrStr string) (net.Conn, error) {
	if strings.TrimSpace(destAddrStr) == "" {
		return nil, fmt.Errorf("empty target address")
	}
	cConn, err := d.dialBase()
	if err != nil {
		return nil, err
	}
	if d.deferInitialOpen() {
		deferred, err := newDeferredKIPOpenConn(cConn, destAddrStr)
		if err != nil {
			_ = cConn.Close()
			return nil, fmt.Errorf("prepare address failed: %w", err)
		}
		return deferred, nil
	}
	if err := writeKIPOpenTCP(cConn, destAddrStr); err != nil {
		_ = cConn.Close()
		return nil, fmt.Errorf("write address failed: %w", err)
	}
	return cConn, nil
}

func (d *BaseDialer) deferInitialOpen() bool {
	if d == nil || d.Config == nil || !d.Config.HTTPMaskTunnelEnabled() {
		return false
	}
	switch strings.TrimSpace(d.Config.HTTPMask.Mode) {
	case "stream", "auto":
		return true
	default:
		return false
	}
}

func (d *BaseDialer) dialUoT() (net.Conn, error) {
	conn, err := d.dialBase()
	if err != nil {
		return nil, err
	}
	if err := WriteKIPMessage(conn, KIPTypeStartUoT, nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("uot preface failed: %w", err)
	}
	return conn, nil
}

// StandardDialer implements Dialer for standard Sudoku mode.
type StandardDialer struct {
	BaseDialer
}

func (d *StandardDialer) Dial(destAddrStr string) (net.Conn, error) {
	return d.dialTarget(destAddrStr)
}

// DialUDPOverTCP establishes a UoT-capable tunnel for UDP proxying.
func (d *StandardDialer) DialUDPOverTCP() (net.Conn, error) {
	return d.dialUoT()
}
