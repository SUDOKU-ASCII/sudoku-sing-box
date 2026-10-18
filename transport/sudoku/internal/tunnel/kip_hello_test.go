package tunnel

import (
	"bytes"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func TestAcceptClientHello(t *testing.T) {
	table := sudoku.NewTable("hello-core", "prefer_ascii")
	tables := []*sudoku.Table{table}
	const psk = "hello-core-psk"

	newHello := func(t *testing.T, feats uint32) (*KIPClientHello, func(*acceptedHello) ([]byte, []byte, error)) {
		t.Helper()
		ch, priv, err := newClientHello(kipUserHashFromPrivateKey(nil, psk), feats, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		return ch, func(a *acceptedHello) ([]byte, []byte, error) {
			return deriveClientSession(psk, priv, ch.Nonce, a.reply)
		}
	}

	t.Run("client and server derive the same session", func(t *testing.T) {
		ch, clientKeys := newHello(t, KIPFeatAll)
		a, err := acceptClientHello(ch, psk, table, tables, nil)
		if err != nil {
			t.Fatal(err)
		}
		c2s, s2c, err := clientKeys(a)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(c2s, a.c2s) || !bytes.Equal(s2c, a.s2c) {
			t.Error("client and server session keys differ")
		}
		if a.table != table || a.userHash != kipUserHashHex(ch.UserHash) {
			t.Errorf("table/userHash = %p/%s", a.table, a.userHash)
		}
	})

	t.Run("reply echoes unknown feature bits", func(t *testing.T) {
		ch, _ := newHello(t, 0xffffffff)
		a, err := acceptClientHello(ch, psk, table, tables, nil)
		if err != nil {
			t.Fatal(err)
		}
		if a.reply.SelectedFeats != 0xffffffff || a.reply.Nonce != ch.Nonce {
			t.Errorf("reply = %+v", a.reply)
		}
	})

	t.Run("stale and future timestamps are rejected", func(t *testing.T) {
		for _, skew := range []time.Duration{-2 * kipHandshakeSkew, 2 * kipHandshakeSkew} {
			ch, _ := newHello(t, KIPFeatAll)
			ch.Timestamp = time.Now().Add(skew)
			if _, err := acceptClientHello(ch, psk, table, tables, nil); err == nil {
				t.Errorf("skew %v accepted", skew)
			}
		}
	})

	t.Run("replay callback decides", func(t *testing.T) {
		ch, _ := newHello(t, KIPFeatAll)
		var seenUser string
		allow := func(user string, nonce [kipHelloNonceSize]byte, _ time.Time) bool {
			seenUser = user
			return false
		}
		if _, err := acceptClientHello(ch, psk, table, tables, allow); err == nil {
			t.Error("replayed hello accepted")
		}
		if seenUser != kipUserHashHex(ch.UserHash) {
			t.Errorf("replay callback saw user %q", seenUser)
		}
	})

	t.Run("unknown table hint is rejected", func(t *testing.T) {
		ch, _ := newHello(t, KIPFeatAll)
		ch.HasTableHint, ch.TableHint = true, table.Hint()+1
		if _, err := acceptClientHello(ch, psk, table, tables, nil); err == nil {
			t.Error("unknown table hint accepted")
		}
	})
}
