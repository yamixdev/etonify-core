package libbox

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/stretchr/testify/require"
)

// TestEtonifyNaiveConfigSchema is intentionally independent of the Cronet
// runtime. Linux, Android and Apple use different Cronet artifacts, so the
// Android integration is verified by the AAR build and device checklist.
func TestEtonifyNaiveConfigSchema(t *testing.T) {
	options, err := json.UnmarshalExtended[option.NaiveOutboundOptions]([]byte(`{
		"server": "example.com",
		"server_port": 443,
		"username": "meow",
		"password": "test-password",
		"quic": true,
		"quic_congestion_control": "bbr",
		"tls": {
			"enabled": true,
			"server_name": "example.com"
		}
	}`))
	require.NoError(t, err)
	require.Equal(t, "example.com", options.Server)
	require.Equal(t, uint16(443), options.ServerPort)
	require.Equal(t, "meow", options.Username)
	require.Equal(t, "test-password", options.Password)
	require.True(t, options.QUIC)
	require.Equal(t, "bbr", options.QUICCongestionControl)
	require.NotNil(t, options.TLS)
	require.True(t, options.TLS.Enabled)
	require.Equal(t, "example.com", options.TLS.ServerName)
}
