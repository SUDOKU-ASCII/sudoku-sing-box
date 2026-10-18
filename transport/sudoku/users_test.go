package sudoku

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	sudokuc "github.com/sagernet/sing-box/transport/sudoku/crypto"
)

type usersTestResult struct {
	session  SessionKind
	target   string
	userHash string
	err      error
}

type usersTestServer struct {
	addr    string
	results chan usersTestResult
	srv     *HTTPMaskTunnelServer
}

func startUsersTestServer(t *testing.T, cfg *ProtocolConfig) *usersTestServer {
	t.Helper()
	srv, err := NewHTTPMaskTunnelServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server := &usersTestServer{addr: listener.Addr().String(), results: make(chan usersTestResult, 8), srv: srv}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				session, kind, target, userHash, _, handled, err := srv.HandleConnSessionAutoWithUserHash(conn)
				if err == nil && !handled {
					return
				}
				if err == nil && session == nil {
					// HTTPMask bookkeeping request, no tunnel session.
					return
				}
				if session != nil {
					defer session.Close()
				}
				server.results <- usersTestResult{session: kind, target: target, userHash: userHash, err: err}
				if err != nil {
					_ = conn.Close()
				}
			}()
		}
	}()
	return server
}

func (s *usersTestServer) next(t *testing.T) usersTestResult {
	t.Helper()
	select {
	case result := <-s.results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("server did not report a session")
		return usersTestResult{}
	}
}

func TestServerUsersWhitelist(t *testing.T) {
	master, err := sudokuc.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKey := sudokuc.EncodePoint(master.Public)
	userKey := func() (string, string) {
		key, err := sudokuc.SplitPrivateKey(master.Private)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := hex.DecodeString(key)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		return key, hex.EncodeToString(sum[:8])
	}
	aliceKey, aliceHash := userKey()
	bobKey, _ := userKey()
	carolKey, carolHash := userKey()
	daveKey, _ := userKey()

	tables, err := NewTablesWithCustomPatterns(publicKey, "prefer_ascii", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, mode := range []struct {
		name        string
		disableMask bool
		maskMode    string
	}{
		{name: "no-httpmask", disableMask: true},
		{name: "legacy", maskMode: "legacy"},
		{name: "stream", maskMode: "stream"},
		{name: "ws", maskMode: "ws"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			base := DefaultConfig()
			base.Tables = tables
			base.PaddingMin = 5
			base.PaddingMax = 15
			base.DisableHTTPMask = mode.disableMask
			base.HTTPMaskMode = mode.maskMode
			if mode.maskMode == "" {
				base.HTTPMaskMode = "legacy"
			}

			serverCfg := *base
			serverCfg.Key = publicKey
			serverCfg.Users = []UserConfig{
				{Alias: "alice", Key: aliceKey},
				{Alias: "bob", Key: bobKey, Blocked: true},
				// Hash prefixes identify a user without keeping its private key around.
				{Key: carolHash[:12]},
				// One byte of quota is used up by the handshake itself.
				{Alias: "dave", Key: daveKey, Data: 1e-9},
			}
			server := startUsersTestServer(t, &serverCfg)

			dial := func(key string) (net.Conn, error) {
				clientCfg := *base
				clientCfg.Key = key
				clientCfg.ServerAddress = server.addr
				clientCfg.TargetAddress = "example.com:443"
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return Dial(ctx, &clientCfg)
			}
			// Some transports leave a rejected client waiting for a reply that never comes, so the
			// outcome is read from the server side while the client gives up in the background.
			attempt := func(key string) {
				go func() {
					if conn, err := dial(key); err == nil {
						_ = conn.Close()
					}
				}()
			}

			// A whitelisted private key opens a session and is reported under its alias.
			aliceConn, err := dial(aliceKey)
			if err != nil {
				t.Fatal(err)
			}
			defer aliceConn.Close()
			result := server.next(t)
			if result.err != nil {
				t.Fatalf("alice rejected: %v", result.err)
			}
			if result.session != SessionForward || result.target != "example.com:443" || result.userHash != aliceHash {
				t.Fatalf("alice session = %+v, want forward example.com:443 %s", result, aliceHash)
			}
			if got := server.srv.UserLabel(result.userHash); got != "alice" {
				t.Fatalf("label = %q, want alice", got)
			}

			// Hash-prefix entries match, and without an alias are labelled by the hash.
			carolConn, err := dial(carolKey)
			if err != nil {
				t.Fatal(err)
			}
			defer carolConn.Close()
			result = server.next(t)
			if result.err != nil || result.userHash != carolHash {
				t.Fatalf("carol session = %+v, want accepted %s", result, carolHash)
			}
			if got := server.srv.UserLabel(carolHash); got != carolHash[:12] {
				t.Fatalf("label = %q, want hash prefix %q", got, carolHash[:12])
			}

			// A blocked user completes the handshake and is turned away by alias.
			attempt(bobKey)
			result = server.next(t)
			var rejected *UserRejectedError
			if !errors.As(result.err, &rejected) || rejected.User != "bob" || rejected.Reason != "blocked" {
				t.Fatalf("bob result = %+v, want blocked rejection", result)
			}
			if strings.Contains(result.err.Error(), bobKey) {
				t.Fatal("rejection must not leak the private key")
			}

			// An unknown key is rejected, and only its hash (never the key) reaches the error.
			unknownKey, unknownHash := userKey()
			attempt(unknownKey)
			result = server.next(t)
			if !errors.As(result.err, &rejected) || rejected.User != unknownHash || rejected.Reason != "not in users whitelist" {
				t.Fatalf("unknown result = %+v, want whitelist rejection for %s", result, unknownHash)
			}
			if strings.Contains(result.err.Error(), unknownKey) {
				t.Fatal("rejection must not leak the private key")
			}

			// The handshake's own bytes exhaust a 1-byte quota. Depending on where the transport
			// counts them the first connection is either cut mid-handshake or established and
			// kicked right after; once the quota is gone every new connection is refused.
			attempt(daveKey)
			server.next(t)
			attempt(daveKey)
			result = server.next(t)
			if result.err == nil {
				t.Fatalf("dave second session = %+v, want it refused after the quota is gone", result)
			}
			if errors.As(result.err, &rejected) && (rejected.User != "dave" || rejected.Reason != "data quota exceeded") {
				t.Fatalf("dave second rejection = %v, want quota rejection", result.err)
			}
		})
	}

	t.Run("no whitelist accepts any key", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Tables = tables
		cfg.Key = publicKey
		cfg.DisableHTTPMask = true
		server := startUsersTestServer(t, cfg)
		clientCfg := *cfg
		clientCfg.Key = aliceKey
		clientCfg.ServerAddress = server.addr
		clientCfg.TargetAddress = "example.com:443"
		conn, err := Dial(context.Background(), &clientCfg)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if result := server.next(t); result.err != nil || result.userHash != aliceHash {
			t.Fatalf("session = %+v, want accepted %s", result, aliceHash)
		}
		if server.srv.HasUsers() {
			t.Fatal("HasUsers must be false without a whitelist")
		}
	})

	t.Run("invalid users fail server construction", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Tables = tables
		cfg.Key = publicKey
		cfg.Users = []UserConfig{{Key: "not-hex"}}
		if _, err := NewHTTPMaskTunnelServer(cfg); err == nil {
			t.Fatal("want an error for a malformed user key")
		}
		cfg.Users = []UserConfig{{Key: aliceKey}, {Key: aliceKey}}
		if _, err := NewHTTPMaskTunnelServer(cfg); err == nil {
			t.Fatal("want an error for a duplicate user")
		}
	})
}

func TestGenMorePrivateKeyKeepsPublicKey(t *testing.T) {
	privateKey, publicKey, err := GenKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	more, morePublic, err := GenMorePrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if morePublic != publicKey {
		t.Fatalf("public key = %s, want %s", morePublic, publicKey)
	}
	if more == privateKey {
		t.Fatal("derived private key must differ from the source key")
	}
	recovered, err := sudokuc.RecoverPublicKey(more)
	if err != nil || sudokuc.EncodePoint(recovered) != publicKey {
		t.Fatalf("derived key does not recover the public key: %v", err)
	}
	if _, _, err := GenMorePrivateKey("zz"); err == nil {
		t.Fatal("want an error for a malformed private key")
	}
}
