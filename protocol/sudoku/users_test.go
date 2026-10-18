package sudoku

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sudokut "github.com/sagernet/sing-box/transport/sudoku"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type usersTestRouter struct {
	adapter.Router
	routed chan adapter.InboundContext
}

func (r *usersTestRouter) RouteConnectionEx(_ context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.routed <- metadata
	_ = conn.Close()
	if onClose != nil {
		onClose(nil)
	}
}

func freeLoopbackPort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func startUsersInbound(t *testing.T, options option.SudokuInboundOptions) (*usersTestRouter, uint16) {
	t.Helper()
	port := freeLoopbackPort(t)
	options.ListenOptions = option.ListenOptions{
		Listen:     common.Ptr(badoption.Addr(netip.MustParseAddr("127.0.0.1"))),
		ListenPort: port,
	}
	router := &usersTestRouter{routed: make(chan adapter.InboundContext, 4)}
	ctx := context.Background()
	logger := log.NewNOPFactory().Logger()
	in, err := NewInbound(ctx, router, logger, "sudoku-in", options)
	require.NoError(t, err)
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	require.NoError(t, in.(adapter.Lifecycle).Start(adapter.StartStateStart, scope))
	return router, port
}

func dialUsersInbound(t *testing.T, port uint16, publicKey, privateKey, target string) {
	t.Helper()
	tables, err := buildProtocolTables(privateKey, "", "", nil, true)
	require.NoError(t, err)
	cfg := sudokut.DefaultConfig()
	cfg.Key = privateKey
	cfg.ServerAddress = M.ParseSocksaddrHostPort("127.0.0.1", port).String()
	cfg.TargetAddress = target
	applyProtocolTables(cfg, tables)
	// The client may be left waiting once the server drops it, so it gives up in the background.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if conn, err := sudokut.Dial(ctx, cfg); err == nil {
			_ = conn.Close()
		}
	}()
}

func TestInboundUsersWhitelist(t *testing.T) {
	privateKey, publicKey, err := sudokut.GenKeyPair()
	require.NoError(t, err)
	otherKey, _, err := sudokut.GenMorePrivateKey(privateKey)
	require.NoError(t, err)

	router, port := startUsersInbound(t, option.SudokuInboundOptions{
		Key:   publicKey,
		Users: []option.SudokuUserOptions{{Alias: "alice", Key: privateKey}},
	})

	// The unlisted client completes the handshake but never reaches the router.
	dialUsersInbound(t, port, publicKey, otherKey, "unlisted.example:443")
	select {
	case metadata := <-router.routed:
		t.Fatalf("unlisted client was routed to %s", metadata.Destination)
	case <-time.After(500 * time.Millisecond):
	}

	dialUsersInbound(t, port, publicKey, privateKey, "alice.example:443")
	select {
	case metadata := <-router.routed:
		require.Equal(t, "alice.example:443", metadata.Destination.String())
		require.Equal(t, "sudoku-in", metadata.Inbound)
	case <-time.After(5 * time.Second):
		t.Fatal("whitelisted client was not routed")
	}
}

func TestInboundUsersInvalid(t *testing.T) {
	_, err := NewInbound(context.Background(), &usersTestRouter{}, log.NewNOPFactory().Logger(), "sudoku-in", option.SudokuInboundOptions{
		Key:   "test_key",
		Users: []option.SudokuUserOptions{{Key: "not-hex"}},
	})
	require.Error(t, err)
}
