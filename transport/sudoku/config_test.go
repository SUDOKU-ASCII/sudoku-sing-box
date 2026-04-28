package sudoku

import (
	"testing"

	sudokuo "github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func TestProtocolConfigAllowsPackedDownlinkWithoutAEAD(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Key = "test-key"
	cfg.AEADMethod = "none"
	cfg.EnablePureDownlink = false
	cfg.Table = sudokuo.NewTable("test-key", "prefer_ascii")

	if err := cfg.Validate(); err != nil {
		t.Fatalf("packed downlink with AEAD none should validate: %v", err)
	}
}
