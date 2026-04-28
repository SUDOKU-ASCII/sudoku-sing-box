package sudoku

import "testing"

func TestNewTableSetDirectionalProbeRotation(t *testing.T) {
	patterns := []string{"xpxvvpvv", "vxpvxvvp"}

	ts, err := NewTableSet("seed", "up_ascii_down_entropy", patterns)
	if err != nil {
		t.Fatalf("build table set failed: %v", err)
	}
	if len(ts.Tables) != len(patterns) {
		t.Fatalf("expected full rotation set, got %d", len(ts.Tables))
	}
	if ts.Tables[0].layout.name != "ascii" {
		t.Fatalf("expected ascii uplink table, got %s", ts.Tables[0].layout.name)
	}
	if peer := ts.Tables[0].OppositeDirection(); peer == nil || peer.layout.name == "ascii" {
		t.Fatalf("expected custom entropy downlink table")
	}

	ts, err = NewTableSet("seed", "up_entropy_down_ascii", patterns)
	if err != nil {
		t.Fatalf("build reverse directional table set failed: %v", err)
	}
	if len(ts.Tables) != len(patterns) {
		t.Fatalf("expected full rotation set, got %d", len(ts.Tables))
	}
}
