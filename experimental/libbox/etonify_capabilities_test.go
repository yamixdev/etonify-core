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
	require.False(t, capabilities.SupportsTargetedURLTest)
	require.False(t, capabilities.SupportsGroupURLTestSessions)
	require.False(t, capabilities.SupportsStructuredProbeErrors)
	require.False(t, capabilities.SupportsOutboundExternalInfo)
	require.False(t, capabilities.SupportsMixedRoutingOutbound)
	require.False(t, capabilities.SupportsURLTestTimeout)
	require.False(t, capabilities.SupportsURLTestConcurrency)
	require.False(t, capabilities.SupportsURLTestDeadline)
	require.False(t, capabilities.SupportsURLTestForce)
	require.False(t, capabilities.SupportsURLTestUnavailableCheckInterval)
	require.False(t, capabilities.SupportsURLTestMethod)
	require.False(t, capabilities.SupportsURLTestInterruptDelayThreshold)
	require.Equal(t, "group_events", capabilities.URLTestCompletionModel)
	require.True(t, capabilities.SupportsConfigCheck)
	require.True(t, capabilities.SupportsCloseConnections)
	require.Equal(t, []string{"system", "gvisor", "mixed"}, capabilities.TUNStacks)
	require.Equal(t, content, EtonifyCapabilities())
}
