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
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	"github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
)

const (
	maxUoTPayload = 64 * 1024
)

// UoTDialer extends Dialer with the ability to bootstrap a UDP-over-TCP tunnel.
type UoTDialer interface {
	Dialer
	DialUDPOverTCP() (net.Conn, error)
}

const maxUoTPending = 256 * 1024

// UoTWriter queues datagram frames and flushes everything queued with one write, so a backlog
// is sent as large records instead of one record and syscall per datagram. When the queue is
// full, datagrams are dropped, as a congested UDP path would.
type UoTWriter struct {
	w       io.Writer
	onErr   func(error)
	mu      sync.Mutex
	pending []byte
	spare   []byte
	err     error
	sig     chan struct{}
	done    chan struct{}
	once    sync.Once
}

// NewUoTWriter starts the flusher; onErr (optional) is called once if a write fails.
func NewUoTWriter(w io.Writer, onErr func(error)) *UoTWriter {
	u := &UoTWriter{w: w, onErr: onErr, sig: make(chan struct{}, 1), done: make(chan struct{})}
	go u.loop()
	return u
}

func (u *UoTWriter) Close() { u.once.Do(func() { close(u.done) }) }

func (u *UoTWriter) loop() {
	for {
		select {
		case <-u.sig:
		case <-u.done:
			return
		}
		for {
			u.mu.Lock()
			buf := u.pending
			if len(buf) == 0 {
				u.mu.Unlock()
				break
			}
			u.pending = u.spare[:0]
			u.spare = nil
			u.mu.Unlock()

			err := connutil.WriteFull(u.w, buf)

			u.mu.Lock()
			if cap(buf) <= 1<<20 {
				u.spare = buf[:0]
			}
			if err != nil {
				u.err = err
				u.pending = nil
			}
			u.mu.Unlock()
			if err != nil {
				if u.onErr != nil {
					u.onErr(err)
				}
				return
			}
		}
	}
}

func (u *UoTWriter) enqueue(appendAddr func([]byte) ([]byte, error), payload []byte) error {
	if len(payload) > int(^uint16(0)) {
		return fmt.Errorf("payload too large: %d", len(payload))
	}
	u.mu.Lock()
	if u.err != nil {
		err := u.err
		u.mu.Unlock()
		return err
	}
	if len(u.pending) >= maxUoTPending {
		u.mu.Unlock()
		return nil
	}
	start := len(u.pending)
	buf := append(u.pending, 0, 0, 0, 0)
	buf, err := appendAddr(buf)
	if err != nil {
		u.pending = buf[:start]
		u.mu.Unlock()
		return fmt.Errorf("encode address: %w", err)
	}
	// An encoded address is at most 1+1+255+2 bytes, so its length always fits the u16 field.
	binary.BigEndian.PutUint16(buf[start:], uint16(len(buf)-start-4))
	binary.BigEndian.PutUint16(buf[start+2:], uint16(len(payload)))
	u.pending = append(buf, payload...)
	u.mu.Unlock()

	select {
	case u.sig <- struct{}{}:
	default:
	}
	return nil
}

// WriteDatagram queues a datagram for addr (host:port).
func (u *UoTWriter) WriteDatagram(addr string, payload []byte) error {
	return u.enqueue(func(b []byte) ([]byte, error) { return protocol.AppendAddress(b, addr) }, payload)
}

// WriteAddrPort queues a datagram for an IP destination, skipping the string round trip of WriteDatagram.
func (u *UoTWriter) WriteAddrPort(ap netip.AddrPort, payload []byte) error {
	return u.enqueue(func(b []byte) ([]byte, error) { return protocol.AppendAddrPort(b, ap), nil }, payload)
}

// UoTReader reads datagram frames from a UoT tunnel into one buffer that is reused across calls.
type UoTReader struct {
	r      io.Reader
	header [4]byte // a field, because a local would escape through the io.Reader call
	buf    []byte
}

func NewUoTReader(r io.Reader) *UoTReader { return &UoTReader{r: r} }

// readUoTFrame reads one frame into buf, growing it (to at least minCap) when it is too small.
// It returns the encoded address, the payload, and the buffer to pass to the next call.
func readUoTFrame(r io.Reader, header *[4]byte, buf []byte, minCap int) (addr, payload, grown []byte, err error) {
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, nil, buf, err
	}
	addrLen := int(binary.BigEndian.Uint16(header[:2]))
	payloadLen := int(binary.BigEndian.Uint16(header[2:]))
	if addrLen == 0 {
		return nil, nil, buf, fmt.Errorf("invalid address length: %d", addrLen)
	}
	n := addrLen + payloadLen
	if cap(buf) < n {
		buf = make([]byte, max(n, minCap))
	}
	body := buf[:n]
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, nil, buf, err
	}
	return body[:addrLen], body[addrLen:], buf, nil
}

// next returns the encoded address and the payload of the next frame; both are valid until the next call.
func (u *UoTReader) next() (addr, payload []byte, err error) {
	addr, payload, u.buf, err = readUoTFrame(u.r, &u.header, u.buf, 2048)
	return addr, payload, err
}

// ReadDatagram returns the datagram's peer as "host:port" and its payload. The payload is
// only valid until the next call.
func (u *UoTReader) ReadDatagram() (string, []byte, error) {
	addrBytes, payload, err := u.next()
	if err != nil {
		return "", nil, err
	}
	return decodeUoTPeer(addrBytes, payload)
}

func decodeUoTPeer(addrBytes, payload []byte) (string, []byte, error) {
	ap, host, err := parseUoTAddr(addrBytes)
	if err != nil {
		return "", nil, fmt.Errorf("decode address: %w", err)
	}
	if host == "" {
		host = ap.String()
	}
	return host, payload, nil
}

// parseUoTAddr decodes an encoded address. IP destinations come back as ap; domains as host (host:port).
func parseUoTAddr(b []byte) (ap netip.AddrPort, host string, err error) {
	if len(b) > 0 {
		switch b[0] {
		case protocol.AddrTypeIPv4:
			if len(b) >= 1+4+2 {
				ip := netip.AddrFrom4([4]byte(b[1:5]))
				return netip.AddrPortFrom(ip, binary.BigEndian.Uint16(b[5:7])), "", nil
			}
		case protocol.AddrTypeIPv6:
			if len(b) >= 1+16+2 {
				ip := netip.AddrFrom16([16]byte(b[1:17])).Unmap()
				return netip.AddrPortFrom(ip, binary.BigEndian.Uint16(b[17:19])), "", nil
			}
		}
	}
	host, err = protocol.DecodeAddress(b)
	return netip.AddrPort{}, host, err
}

// ReadUoTDatagram parses a single UDP datagram frame from the reliable tunnel. The payload is
// owned by the caller; loops should use a UoTReader instead.
func ReadUoTDatagram(r io.Reader) (string, []byte, error) {
	var header [4]byte
	addrBytes, payload, _, err := readUoTFrame(r, &header, nil, 0)
	if err != nil {
		return "", nil, err
	}
	return decodeUoTPeer(addrBytes, payload)
}

const (
	// uotDestinationTTL bounds how long a resolved domain is reused for datagram destinations.
	uotDestinationTTL        = 30 * time.Second
	uotDestinationCacheLimit = 256
)

type uotDestination struct {
	ap      netip.AddrPort
	expires time.Time
}

// uotDestinations resolves the destination of each datagram. Domains are cached for a short time
// (successful lookups only), keyed by their encoded form so a hit does not even decode the name.
// It belongs to the single goroutine that reads the tunnel and needs no lock.
type uotDestinations struct {
	resolve func(hostPort string) (netip.AddrPort, error)
	cache   map[string]uotDestination
}

func newUoTDestinations() *uotDestinations {
	return &uotDestinations{resolve: resolveUDPAddrPort, cache: make(map[string]uotDestination)}
}

func resolveUDPAddrPort(hostPort string) (netip.AddrPort, error) {
	a, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return a.AddrPort(), nil
}

func (d *uotDestinations) lookup(addr []byte, now time.Time) (netip.AddrPort, error) {
	if len(addr) > 0 && addr[0] == protocol.AddrTypeDomain {
		if e, ok := d.cache[string(addr)]; ok {
			if now.Before(e.expires) {
				return e.ap, nil
			}
			delete(d.cache, string(addr))
		}
	}
	ap, host, err := parseUoTAddr(addr)
	if err != nil || host == "" {
		return ap, err
	}
	ap, err = d.resolve(host)
	if err != nil {
		return netip.AddrPort{}, err
	}
	d.store(string(addr), ap, now)
	return ap, nil
}

func (d *uotDestinations) store(key string, ap netip.AddrPort, now time.Time) {
	if len(d.cache) >= uotDestinationCacheLimit {
		for k, e := range d.cache {
			if !now.Before(e.expires) {
				delete(d.cache, k)
			}
		}
		if len(d.cache) >= uotDestinationCacheLimit {
			clear(d.cache)
		}
	}
	d.cache[key] = uotDestination{ap: ap, expires: now.Add(uotDestinationTTL)}
}

// HandleUoTServer bridges UDP packets over the already-upgraded tunnel connection.
func HandleUoTServer(conn net.Conn) error {
	pConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return fmt.Errorf("listen udp for uot: %w", err)
	}

	errCh := make(chan error, 1)
	var once sync.Once

	closeAll := func(err error) {
		once.Do(func() {
			_ = conn.Close()
			_ = pConn.Close()
			errCh <- err
		})
	}

	uw := NewUoTWriter(conn, closeAll)
	defer uw.Close()

	go func() {
		buf := make([]byte, maxUoTPayload)
		for {
			n, ap, err := pConn.ReadFromUDPAddrPort(buf)
			if err != nil {
				closeAll(err)
				return
			}
			if err := uw.WriteAddrPort(ap, buf[:n]); err != nil {
				closeAll(err)
				return
			}
		}
	}()

	go func() {
		fr := NewUoTReader(conn)
		dests := newUoTDestinations()
		for {
			addrBytes, payload, err := fr.next()
			if err != nil {
				closeAll(err)
				return
			}
			ap, err := dests.lookup(addrBytes, time.Now())
			if err != nil {
				// Skip invalid or unresolvable destinations instead of failing the whole session.
				continue
			}
			if _, err := pConn.WriteToUDPAddrPort(payload, ap); err != nil {
				closeAll(err)
				return
			}
		}
	}()

	return <-errCh
}
