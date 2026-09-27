package sudoku

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var _ N.PacketConn = (*UoTPacketConn)(nil)

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
		target := M.ParseSocksaddr(addrStr)
		if !target.IsValid() || target.Port == 0 {
			continue
		}
		copy(p, payload)
		if target.IsIP() {
			return len(payload), net.UDPAddrFromAddrPort(netip.AddrPortFrom(target.Addr.Unmap(), target.Port)), nil
		}
		return len(payload), target, nil
	}
}

func (c *UoTPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	for {
		addr, payload, err := internaltunnel.ReadUoTDatagram(c.conn)
		if err != nil {
			return M.Socksaddr{}, err
		}
		target := M.ParseSocksaddr(addr)
		if !target.IsValid() || target.Port == 0 {
			continue
		}
		_, err = buffer.Write(payload)
		return target.Unwrap(), err
	}
}

func (c *UoTPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	_, err := c.WriteTo(buffer.Bytes(), destination)
	return err
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
