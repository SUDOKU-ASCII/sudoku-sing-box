package sudoku

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestResolveMultiplexPrecedence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		topLevel string
		legacy   string
		want     string
	}{
		{name: "default", want: "off"},
		{name: "top-level", topLevel: " ON ", want: "on"},
		{name: "legacy", legacy: " AUTO ", want: "auto"},
		{name: "legacy-wins", topLevel: "on", legacy: "off", want: "off"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveMultiplex("off", test.topLevel, test.legacy); got != test.want {
				t.Fatalf("resolveMultiplex() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSudokuOptionsMultiplexJSONCompatibility(t *testing.T) {
	t.Parallel()

	var outbound option.SudokuOutboundOptions
	err := json.Unmarshal([]byte(`{
		"multiplex": "on",
		"http_mask_multiplex": "auto",
		"httpmask": {
			"disable": false,
			"mode": "stream",
			"multiplex": "off"
		}
	}`), &outbound)
	if err != nil {
		t.Fatalf("unmarshal outbound options: %v", err)
	}
	if outbound.Multiplex != "on" {
		t.Fatalf("top-level multiplex = %q, want on", outbound.Multiplex)
	}
	if outbound.HTTPMaskMultiplex != "off" {
		t.Fatalf("legacy multiplex = %q, want nested value off", outbound.HTTPMaskMultiplex)
	}
	if got := resolveMultiplex("off", outbound.Multiplex, outbound.HTTPMaskMultiplex); got != "off" {
		t.Fatalf("resolved multiplex = %q, want off", got)
	}
}
