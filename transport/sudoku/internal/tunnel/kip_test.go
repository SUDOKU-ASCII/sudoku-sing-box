package tunnel

import (
	"testing"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

func TestKIPClientHelloTableHintRoundTrip(t *testing.T) {
	hello := &KIPClientHello{
		Features:     KIPFeatAll,
		TableHint:    7,
		HasTableHint: true,
	}

	decoded, err := DecodeKIPClientHelloPayload(hello.EncodePayload())
	if err != nil {
		t.Fatalf("decode hello failed: %v", err)
	}
	if !decoded.HasTableHint {
		t.Fatalf("expected table hint to be preserved")
	}
	if decoded.TableHint != hello.TableHint {
		t.Fatalf("unexpected table hint: got %d want %d", decoded.TableHint, hello.TableHint)
	}
}

func TestResolveClientHelloTableAllowsDirectionalASCIIRotation(t *testing.T) {
	tables := make([]*sudoku.Table, 0, 2)
	for _, pattern := range []string{"xpxvvpvv", "vxpvxvvp"} {
		table, err := sudoku.NewTableWithCustom("hint-seed", "up_ascii_down_entropy", pattern)
		if err != nil {
			t.Fatalf("build table failed: %v", err)
		}
		tables = append(tables, table)
	}

	selected, err := ResolveClientHelloTable(tables[0], tables, &KIPClientHello{
		TableHint:    tables[1].Hint(),
		HasTableHint: true,
	})
	if err != nil {
		t.Fatalf("resolve hinted table failed: %v", err)
	}
	if selected != tables[1] {
		t.Fatalf("resolved wrong table: got %p want %p", selected, tables[1])
	}
}
