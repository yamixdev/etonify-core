package libbox

import (
	"encoding/json"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/stretchr/testify/require"
)

func TestEtonifyCapabilities(t *testing.T) {
	t.Parallel()

	content := EtonifyCapabilities()
	var capabilities etonifyCapabilitySet
	require.NoError(t, json.Unmarshal([]byte(content), &capabilities))
	require.Equal(t, etonifyAPIVersion, capabilities.APIVersion)
	require.Equal(t, C.Version, capabilities.CoreVersion)
	require.True(t, capabilities.SupportsTargetedURLTest)
	require.True(t, capabilities.SupportsGroupURLTestSessions)
	require.True(t, capabilities.SupportsStructuredProbeErrors)
	require.True(t, capabilities.SupportsOutboundExternalInfo)
	require.False(t, capabilities.SupportsMixedRoutingOutbound)
	require.True(t, capabilities.SupportsURLTestTimeout)
	require.True(t, capabilities.SupportsURLTestConcurrency)
	require.True(t, capabilities.SupportsURLTestDeadline)
	require.True(t, capabilities.SupportsURLTestForce)
	require.False(t, capabilities.SupportsURLTestUnavailableCheckInterval)
	require.False(t, capabilities.SupportsURLTestMethod)
	require.False(t, capabilities.SupportsURLTestInterruptDelayThreshold)
	require.Equal(t, "group_events", capabilities.URLTestCompletionModel)
	require.True(t, capabilities.SupportsConfigCheck)
	require.True(t, capabilities.SupportsCloseConnections)
	require.Equal(t, []string{"system", "gvisor", "mixed"}, capabilities.TUNStacks)
	require.Equal(t, content, EtonifyCapabilities())
}
