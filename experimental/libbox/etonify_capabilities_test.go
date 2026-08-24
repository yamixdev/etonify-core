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
	require.True(t, capabilities.SupportsOutboundHTTPFetch)
	require.False(t, capabilities.SupportsMixedRoutingOutbound)
	require.True(t, capabilities.SupportsURLTestTimeout)
	require.True(t, capabilities.SupportsURLTestConcurrency)
	require.True(t, capabilities.SupportsURLTestDeadline)
	require.True(t, capabilities.SupportsURLTestForce)
	require.True(t, capabilities.SupportsURLTestFailover)
	require.False(t, capabilities.SupportsURLTestUnavailableCheckInterval)
	require.False(t, capabilities.SupportsURLTestMethod)
	require.False(t, capabilities.SupportsURLTestInterruptDelayThreshold)
	require.Equal(t, "group_events", capabilities.URLTestCompletionModel)
	require.True(t, capabilities.SupportsConfigCheck)
	require.True(t, capabilities.SupportsCloseConnections)
	require.True(t, capabilities.SupportsRealitySpiderX)
	require.True(t, capabilities.SupportsXHTTP)
	require.True(t, capabilities.SupportsSplitHTTPAlias)
	require.True(t, capabilities.XHTTPClientOnly)
	require.Equal(t, "etonify_client_v1", capabilities.XHTTPProfile)
	require.Equal(t, []string{"packet-up", "stream-up", "stream-one"}, capabilities.XHTTPModes)
	require.Equal(t, 16, capabilities.XHTTPMaxPoolConnections)
	require.Equal(t, 256*1024, capabilities.XHTTPMaxPacketUploadBytes)
	require.True(t, capabilities.SupportsXHTTPCloseAll)
	require.True(t, capabilities.SupportsVLESSEncryption)
	require.True(t, capabilities.VLESSEncryptionClientOnly)
	require.Equal(t, []string{"1rtt", "0rtt", "native", "xorpub", "random", "x25519", "mlkem768"}, capabilities.VLESSEncryptionModes)
	require.Equal(t, 8, capabilities.VLESSEncryptionMaxRelays)
	require.Equal(t, 12_000, capabilities.VLESSEncryptionHandshakeTimeoutMS)
	require.Equal(t, []string{"system", "gvisor", "mixed"}, capabilities.TUNStacks)
	require.Equal(t, content, EtonifyCapabilities())
}
