package option

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutboundRealityOptionsAcceptSpiderX(t *testing.T) {
	t.Parallel()

	var options OutboundTLSOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"enabled":true,
		"reality":{
			"enabled":true,
			"public_key":"test-public-key",
			"short_id":"ab01",
			"spider_x":"/assets?ed=2560"
		}
	}`), &options))
	require.NotNil(t, options.Reality)
	require.Equal(t, "/assets?ed=2560", options.Reality.SpiderX)

	content, err := json.Marshal(options)
	require.NoError(t, err)
	require.Contains(t, string(content), `"spider_x":"/assets?ed=2560"`)
}
