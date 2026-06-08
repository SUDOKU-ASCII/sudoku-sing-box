package option

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSudokuOptionsMultiplexCompatibility(t *testing.T) {
	t.Parallel()

	var outbound SudokuOutboundOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"server": "example.com",
		"server_port": 443,
		"key": "k",
		"multiplex": "on",
		"httpmask": {
			"disable": true
		}
	}`), &outbound))
	require.Equal(t, "on", outbound.Multiplex)
	require.Equal(t, "on", outbound.HTTPMaskMultiplex)

	var inbound SudokuInboundOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"key": "k",
		"multiplex": "on",
		"httpmask": {
			"mode": "stream",
			"multiplex": "auto"
		}
	}`), &inbound))
	require.Equal(t, "on", inbound.Multiplex)
	require.Equal(t, "auto", inbound.HTTPMaskMultiplex)
}
