package sudoku

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	sudokut "github.com/sagernet/sing-box/transport/sudoku"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type uotTestRouter struct {
	destinations chan M.Socksaddr
	errors       chan error
}

func (*uotTestRouter) RouteConnection(context.Context, net.Conn, adapter.InboundContext) error {
	return nil
}

func (*uotTestRouter) RoutePacketConnection(context.Context, N.PacketConn, adapter.InboundContext) error {
	return nil
}

func (*uotTestRouter) RouteConnectionEx(context.Context, net.Conn, adapter.InboundContext, N.CloseHandlerFunc) {
}

func (r *uotTestRouter) RoutePacketConnectionEx(_ context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.destinations <- metadata.Destination
	go func() {
		defer conn.Close()
		defer onClose(nil)
		packet := buf.NewPacket()
		from, err := conn.ReadPacket(packet)
		if err != nil {
			packet.Release()
			r.errors <- err
			return
		}
		if from != metadata.Destination {
			packet.Release()
			r.errors <- io.ErrUnexpectedEOF
			return
		}
		r.errors <- conn.WritePacket(packet, from)
	}()
}

func TestUoTSessionRoutesEachDestination(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	router := &uotTestRouter{
		destinations: make(chan M.Socksaddr, 3),
		errors:       make(chan error, 3),
	}
	inbound := &Inbound{router: router, logger: logger.NOP()}
	done := make(chan struct{})
	go func() {
		inbound.serveUoT(context.Background(), sudokut.NewUoTPacketConn(server), adapter.InboundContext{
			Source: M.ParseSocksaddr("192.0.2.1:12345"),
		}, nil)
		close(done)
	}()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	for _, target := range []string{"198.51.100.1:53", "[2001:db8::1]:123", "example.com:443"} {
		if err := sudokut.WriteDatagram(client, target, []byte("ping")); err != nil {
			t.Fatal(err)
		}
		address, payload, err := sudokut.ReadDatagram(client)
		if err != nil {
			t.Fatal(err)
		}
		if address != target || string(payload) != "ping" {
			t.Fatalf("response = %s %q, want %s ping", address, payload, target)
		}
		if got := <-router.destinations; got != M.ParseSocksaddr(target) {
			t.Fatalf("routed destination = %s, want %s", got, target)
		}
		if err := <-router.errors; err != nil {
			t.Fatal(err)
		}
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UoT session did not close")
	}
}

func TestUoTSessionClosesOnContextCancel(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	inbound := &Inbound{logger: logger.NOP()}
	go func() {
		inbound.serveUoT(ctx, sudokut.NewUoTPacketConn(server), adapter.InboundContext{}, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UoT session did not close after context cancellation")
	}
}
