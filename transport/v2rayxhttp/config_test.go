package v2rayxhttp

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

func TestConfigClampsSubscriptionControlledAllocations(t *testing.T) {
	t.Parallel()

	config := newConfig(option.V2RayXHTTPOptions{
		XPaddingBytes: &option.V2RayXHTTPRangeConfig{
			From: 1 << 30,
			To:   1 << 30,
		},
		UplinkChunkSize: &option.V2RayXHTTPRangeConfig{
			From: 1 << 30,
			To:   1 << 30,
		},
		SessionTable: "Base62",
		SessionLength: &option.V2RayXHTTPRangeConfig{
			From: 1 << 30,
			To:   1 << 30,
		},
		ScMaxEachPostBytes: &option.V2RayXHTTPRangeConfig{
			From: 1 << 30,
			To:   1 << 30,
		},
		ScMinPostsIntervalMs: &option.V2RayXHTTPRangeConfig{
			From: 1 << 30,
			To:   1 << 30,
		},
	})
	require.Equal(t, int32(maxXPaddingBytes), config.xPaddingBytesTo)
	require.Equal(t, int32(maxUplinkChunkBytes), config.uplinkChunkSizeTo)
	require.Equal(t, int32(maxSessionIDLength), config.sessionLengthTo)
	require.Equal(t, int32(maxPacketUploadBytes), config.scMaxEachPostBytesTo)
	require.Equal(t, int32(maxPostsIntervalMillis), config.scMinPostsIntervalMsTo)

	sessionID, err := config.generateSessionID()
	require.NoError(t, err)
	require.Len(t, sessionID, maxSessionIDLength)
}

func TestConfigRejectsUnsupportedPlacements(t *testing.T) {
	t.Parallel()

	config := newConfig(option.V2RayXHTTPOptions{UplinkDataPlacement: "unsupported"})
	require.ErrorContains(t, config.validate(), "uplink data placement")

	config = newConfig(option.V2RayXHTTPOptions{
		XPaddingObfsMode:  true,
		XPaddingPlacement: PlacementHeader,
	})
	require.ErrorContains(t, config.validate(), "padding header")
}
