//go:build !windows && with_naive_outbound

package libbox

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEtonifyNaiveConfigCompatibility(t *testing.T) {
	config := etonifyConfig(nil, []any{
		map[string]any{
			"type":      "selector",
			"tag":       "select",
			"outbounds": []string{"naive"},
			"default":   "naive",
		},
		map[string]any{
			"type":        "naive",
			"tag":         "naive",
			"server":      "example.com",
			"server_port": 443,
			"username":    "meow",
			"password":    "test-password",
			"tls":         etonifyTLS(),
		},
		etonifyDirect(),
	})
	content, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, CheckConfig(string(content)))
}
