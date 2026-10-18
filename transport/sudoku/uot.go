package sudoku

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
	internalprotocol "github.com/sagernet/sing-box/transport/sudoku/internal/protocol"
	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var _ N.PacketConn = (*UoTPacketConn)(nil)

func WritePreface(w net.Conn) error {
	return internaltunnel.WriteKIPMessage(w, internaltunnel.KIPTypeStartUoT, nil)
}

// WriteDatagram writes one UoT frame with a single write. UoTPacketConn batches
// its own writes; this helper is for callers that drive the raw tunnel directly.
func WriteDatagram(w net.Conn, addr string, payload []byte) error {
	if len(payload) > int(^uint16(0)) {
		return fmt.Errorf("payload too large: %d", len(payload))
	}
	frame, err := internalprotocol.AppendAddress(make([]byte, 4, 4+len(addr)+len(payload)+8), addr)
	if err != nil {
		return fmt.Errorf("encode address: %w", err)
	}
	// An encoded address is at most 1+1+255+2 bytes, so its length always fits the u16 field.
	binary.BigEndian.PutUint16(frame, uint16(len(frame)-4))
	binary.BigEndian.PutUint16(frame[2:], uint16(len(payload)))
	return connutil.WriteFull(w, append(frame, payload...))
}

func ReadDatagram(r net.Conn) (string, []byte, error) {
	return internaltunnel.ReadUoTDatagram(r)
}

// UoTPacketConn carries UDP datagrams over a Sudoku UoT tunnel. Reads reuse one
// frame buffer and writes are queued and flushed together, so bursts leave as
// large records instead of one record per datagram.
type UoTPacketConn struct {
	conn   net.Conn
	reader *internaltunnel.UoTReader
	readMu sync.Mutex
	writer *internaltunnel.UoTWriter
}

func NewUoTPacketConn(conn net.Conn) *UoTPacketConn {
	return &UoTPacketConn{
		conn:   conn,
		reader: internaltunnel.NewUoTReader(conn),
		writer: internaltunnel.NewUoTWriter(conn, func(error) { _ = conn.Close() }),
	}
}

func (c *UoTPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		addrStr, payload, err := c.reader.ReadDatagram()
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
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		addr, payload, err := c.reader.ReadDatagram()
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
	var err error
	switch destination := addr.(type) {
	case *net.UDPAddr:
		err = c.writer.WriteAddrPort(destination.AddrPort(), p)
	case M.Socksaddr:
		if destination.IsIP() {
			err = c.writer.WriteAddrPort(destination.AddrPort(), p)
		} else {
			err = c.writer.WriteDatagram(destination.String(), p)
		}
	default:
		err = c.writer.WriteDatagram(addr.String(), p)
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *UoTPacketConn) Close() error {
	c.writer.Close()
	return c.conn.Close()
}

func (c *UoTPacketConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *UoTPacketConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *UoTPacketConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *UoTPacketConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
