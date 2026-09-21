package v2rayxhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

type xrayReferenceFixture struct {
	Revision  string `json:"revision"`
	AutoModes []struct {
		Name     string `json:"name"`
		Reality  bool   `json:"reality"`
		Resolved string `json:"resolved"`
	} `json:"auto_modes"`
	XmuxDefaults struct {
		MaxConnections    int   `json:"max_connections"`
		MinRequests       int32 `json:"min_requests"`
		MaxRequests       int32 `json:"max_requests"`
		MinReusableSecond int64 `json:"min_reusable_seconds"`
		MaxReusableSecond int64 `json:"max_reusable_seconds"`
	} `json:"xmux_defaults"`
	HTTP2Defaults struct {
		ReadIdleSeconds      int64 `json:"read_idle_seconds"`
		IdleConnectionSecond int64 `json:"idle_connection_seconds"`
	} `json:"http2_defaults"`
}

func TestXrayReferencePolicyFixture(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("testdata/xray-reference.json")
	require.NoError(t, err)
	var fixture xrayReferenceFixture
	require.NoError(t, json.Unmarshal(content, &fixture))
	require.NotEmpty(t, fixture.Revision)

	for _, testCase := range fixture.AutoModes {
		t.Run(testCase.Name, func(t *testing.T) {
			require.Equal(t, testCase.Resolved, resolveMode("auto", testCase.Reality))
		})
	}

	manager := newXmuxManager(&option.V2RayXHTTPXmuxConfig{}, func() *http.Client {
		return &http.Client{Transport: rejectingRoundTripper{}}
	})
	require.Equal(t, fixture.XmuxDefaults.MaxConnections, manager.desiredConnections)
	require.Equal(t, fixture.XmuxDefaults.MaxConnections, manager.maxConnections)
	for range fixture.XmuxDefaults.MaxConnections {
		lease, acquireErr := manager.acquire(context.Background())
		require.NoError(t, acquireErr)
		manager.access.Lock()
		require.GreaterOrEqual(t, lease.client.leftRequests, fixture.XmuxDefaults.MinRequests)
		require.LessOrEqual(t, lease.client.leftRequests, fixture.XmuxDefaults.MaxRequests)
		reusableFor := time.Until(lease.client.unreusableAt)
		require.GreaterOrEqual(t, reusableFor, time.Duration(fixture.XmuxDefaults.MinReusableSecond)*time.Second-time.Second)
		require.LessOrEqual(t, reusableFor, time.Duration(fixture.XmuxDefaults.MaxReusableSecond)*time.Second)
		manager.access.Unlock()
		lease.release()
	}
	manager.closeAll()

	require.Equal(t, time.Duration(fixture.HTTP2Defaults.ReadIdleSeconds)*time.Second, defaultH2ReadIdle)
	require.Equal(t, time.Duration(fixture.HTTP2Defaults.IdleConnectionSecond)*time.Second, defaultHTTPIdle)
}
