package option

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestV2RayXHTTPOptionsAcceptScalarAndRangeValues(t *testing.T) {
	t.Parallel()

	var options V2RayTransportOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"xhttp",
		"path":"/transport",
		"mode":"packet-up",
		"sc_max_each_post_bytes":65536,
		"xmux":{
			"max_concurrency":{"from":2,"to":4},
			"max_connections":3
		}
	}`), &options))
	require.Equal(t, "xhttp", options.Type)
	require.Equal(t, int32(65536), options.XHTTPOptions.ScMaxEachPostBytes.From)
	require.Equal(t, int32(65536), options.XHTTPOptions.ScMaxEachPostBytes.To)
	require.Equal(t, int32(2), options.XHTTPOptions.Xmux.MaxConcurrency.From)
	require.Equal(t, int32(4), options.XHTTPOptions.Xmux.MaxConcurrency.To)
	require.Equal(t, int32(3), options.XHTTPOptions.Xmux.MaxConnections.From)
	require.Equal(t, int32(3), options.XHTTPOptions.Xmux.MaxConnections.To)

	content, err := json.Marshal(options)
	require.NoError(t, err)
	var roundTrip V2RayTransportOptions
	require.NoError(t, json.Unmarshal(content, &roundTrip))
	require.Equal(t, options.XHTTPOptions, roundTrip.XHTTPOptions)
}

func TestV2RaySplitHTTPUsesXHTTPOptions(t *testing.T) {
	t.Parallel()

	var options V2RayTransportOptions
	require.NoError(t, json.Unmarshal([]byte(`{"type":"splithttp","mode":"stream-up"}`), &options))
	require.Equal(t, "splithttp", options.Type)
	require.Equal(t, "stream-up", options.XHTTPOptions.Mode)
}

func TestV2RayXHTTPOptionsAcceptStringRanges(t *testing.T) {
	t.Parallel()

	var options V2RayTransportOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"type":"xhttp",
		"xmux":{
			"max_concurrency":"16-32",
			"h_max_reusable_secs":"1800-3000"
		}
	}`), &options))
	require.Equal(t, &V2RayXHTTPRangeConfig{From: 16, To: 32}, options.XHTTPOptions.Xmux.MaxConcurrency)
	require.Equal(t, &V2RayXHTTPRangeConfig{From: 1800, To: 3000}, options.XHTTPOptions.Xmux.HMaxReusableSecs)

	require.Error(t, json.Unmarshal([]byte(`{"type":"xhttp","x_padding_bytes":"invalid"}`), &options))
}
