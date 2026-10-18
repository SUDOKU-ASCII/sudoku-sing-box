package sudoku

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"testing"
)

// These digests pin this implementation's own wire stream, including padding,
// protected prefixes and residual-bit markers, for a fixed random seed. They
// guard against accidental drift; they are not the v0.5.1 contract, which
// the reference vector tests hold.
func TestCodecWireCompatibility(t *testing.T) {
	goldens := map[string]string{
		"prefer_ascii/packed=false/padding=0":     "729f13e04bc3c0bfd9415f0f5b098abab77d2e11ffafe975f1d59bc589435067",
		"prefer_ascii/packed=false/padding=10":    "a6c2c4a46ce49e2c52a4d639f1ed75b58cda363b0c15592fc7b800ac1e227a54",
		"prefer_ascii/packed=false/padding=100":   "43321f86ed4237649593971b2065845f60394e36bc6a9fa26af1629896b98d0f",
		"prefer_ascii/packed=true/padding=0":      "9eda3a7a9ebf9a6349ae17a87ae22f2bc604108eeb303ea270018c739e104974",
		"prefer_ascii/packed=true/padding=10":     "714c1ce842e37da14ef4581eb97e207e31ddd437204a31db40128c6410bf5d71",
		"prefer_ascii/packed=true/padding=100":    "55b6c22153e6be412197fcb6a149a7f24662cadb7259a4149e4593c6b07ecbcc",
		"prefer_entropy/packed=false/padding=0":   "70c1ae4785c2cf315f6ed1176d1e0d50c31e0ec116b086e8266574570af329d1",
		"prefer_entropy/packed=false/padding=10":  "4117df64a2ce299ac5e2117b272341fb8a638afae11b920925d57fea24ec0bbd",
		"prefer_entropy/packed=false/padding=100": "27c6cbca243ff429159b4aaed138a2a6f4efc0cd181e63a068369133473f69b4",
		"prefer_entropy/packed=true/padding=0":    "c52de046d46f2dbaa5a53df56c7c1c3567ec4a1b501e656f36513c00fc37e0ae",
		"prefer_entropy/packed=true/padding=10":   "763c16c1110716306b25b38b0cf13ed3c38eea2b67d944fc76504594fea124d9",
		"prefer_entropy/packed=true/padding=100":  "51c8863c722ed38b27040d95c4114ea399339c7cb16376421b187c04de789ce8",
		"custom/packed=false/padding=0":           "b5991e05b444611d2152c8a812d56facb27c7852ab8dd49bb5b6fda419e1bf14",
		"custom/packed=false/padding=10":          "5af853d23f1fa58c6d0c4d070ebfff2885874e04ffa49524561ecf5af85d0e7e",
		"custom/packed=false/padding=100":         "dd307a3ba6239a0c16ffaf37832237741d6ecae1a0e8e584bf74ea8310258f39",
		"custom/packed=true/padding=0":            "4ecc515b7cdefee2f551d62306e874eedf6e715df86daf014a365e6fcaf9db39",
		"custom/packed=true/padding=10":           "540418a5ae9a6471f10bc86954c7ed809624a5c2226c768d58acd5f5b9c33396",
		"custom/packed=true/padding=100":          "9260da0e190871e3c081a5c35c376bcf5c839f2cd44d3e455ceb7fdf137ba43e",
	}

	for _, mode := range []string{"prefer_ascii", "prefer_entropy", "custom"} {
		preference, pattern := mode, ""
		if mode == "custom" {
			preference, pattern = "prefer_entropy", "xvpvvpxv"
		}
		table, err := NewTableWithCustom("wire-compatibility", preference, pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, packed := range []bool{false, true} {
			for _, padding := range []int{0, 10, 100} {
				name := fmt.Sprintf("%s/packed=%v/padding=%d", mode, packed, padding)
				t.Run(name, func(t *testing.T) {
					var wire bytes.Buffer
					var writer net.Conn
					if packed {
						c := NewPackedConn(writeOnlyConn{Writer: &wire}, table, padding, padding)
						c.rng = newSudokuRand(123456)
						writer = c
					} else {
						c := NewConn(writeOnlyConn{Writer: &wire}, table, padding, padding, false)
						c.rng = newSudokuRand(123456)
						writer = c
					}
					var want []byte
					for _, size := range []int{0, 1, 2, 3, 13, 14, 15, 16, 4097, 65537} {
						p := make([]byte, size)
						for i := range p {
							p[i] = byte(i*31 + i>>8)
						}
						if _, err := writer.Write(p); err != nil {
							t.Fatal(err)
						}
						want = append(want, p...)
					}
					digest := fmt.Sprintf("%x", sha256.Sum256(wire.Bytes()))
					if digest != goldens[name] {
						t.Fatal("encoded bytes differ from the pinned wire sample")
					}
					var reader net.Conn
					raw := &fragmentConn{data: wire.Bytes(), limit: 127}
					if packed {
						reader = NewPackedConn(raw, table, padding, padding)
					} else {
						reader = NewConn(raw, table, padding, padding, false)
					}
					got, err := io.ReadAll(reader)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("wire decode mismatch: %v", err)
					}
				})
			}
		}
	}
}
