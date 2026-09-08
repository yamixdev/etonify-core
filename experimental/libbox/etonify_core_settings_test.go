//go:build with_gvisor && with_quic && with_utls

package libbox

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// These exact configs are also generated and compared by the Flutter tests.
func TestEtonifyAndroidConfigCorpusCoreSettings(t *testing.T) {
	content, err := os.ReadFile("testdata/etonify_core_settings.json")
	require.NoError(t, err)
	var cases []struct {
		Name     string          `json:"name"`
		Expected json.RawMessage `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(content, &cases))
	require.NotEmpty(t, cases)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			require.NoError(t, CheckConfig(string(tc.Expected)))
		})
	}
}
