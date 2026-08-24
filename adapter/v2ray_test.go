package adapter

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResetV2RayClientTransportUsesResetWhenSupported(t *testing.T) {
	transport := &testV2RayTransport{}

	ResetV2RayClientTransport(transport)

	require.Equal(t, 1, transport.resetCalls)
	require.Zero(t, transport.closeCalls)
}

func TestResetV2RayClientTransportClosesLegacyTransport(t *testing.T) {
	transport := &legacyV2RayTransport{}

	ResetV2RayClientTransport(transport)

	require.Equal(t, 1, transport.closeCalls)
}

type legacyV2RayTransport struct {
	closeCalls int
}

func (t *legacyV2RayTransport) DialContext(context.Context) (net.Conn, error) {
	return nil, nil
}

func (t *legacyV2RayTransport) Close() error {
	t.closeCalls++
	return nil
}

type testV2RayTransport struct {
	legacyV2RayTransport
	resetCalls int
}

func (t *testV2RayTransport) Reset() {
	t.resetCalls++
}
