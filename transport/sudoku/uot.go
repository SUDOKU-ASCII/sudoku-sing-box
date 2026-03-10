package sudoku

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
)

func WritePreface(w net.Conn) error {
	return internaltunnel.WriteKIPMessage(w, internaltunnel.KIPTypeStartUoT, nil)
}

func WriteDatagram(w net.Conn, addr string, payload []byte) error {
	return internaltunnel.WriteUoTDatagram(w, addr, payload)
}

func ReadDatagram(r net.Conn) (string, []byte, error) {
	return internaltunnel.ReadUoTDatagram(r)
}

type UoTPacketConn struct {
	conn    net.Conn
	writeMu sync.Mutex
}

func NewUoTPacketConn(conn net.Conn) *UoTPacketConn {
	return &UoTPacketConn{conn: conn}
}

func (c *UoTPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		addrStr, payload, err := internaltunnel.ReadUoTDatagram(c.conn)
		if err != nil {
			return 0, nil, err
		}
		if len(payload) > len(p) {
			return 0, nil, net.ErrWriteToConnected
		}
		host, port, err := net.SplitHostPort(addrStr)
		if err != nil {
			continue
		}
		portInt, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			continue
		}
		udpAddr := net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip.Unmap(), uint16(portInt)))
		copy(p, payload)
		return len(payload), udpAddr, nil
	}
}

func (c *UoTPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if addr == nil {
		return 0, errors.New("address is nil")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := internaltunnel.WriteUoTDatagram(c.conn, addr.String(), p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *UoTPacketConn) Close() error                       { return c.conn.Close() }
func (c *UoTPacketConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *UoTPacketConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *UoTPacketConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *UoTPacketConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
