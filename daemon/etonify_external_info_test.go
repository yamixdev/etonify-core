package daemon

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type externalInfoRoundTripper func(*http.Request) (*http.Response, error)

func (f externalInfoRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestParseOutboundExternalInfo(t *testing.T) {
	info, err := parseOutboundExternalInfo([]byte("ip=203.0.113.4\nloc=nl\n"))
	require.NoError(t, err)
	require.Equal(t, "203.0.113.4", info.ip)
	require.Equal(t, "NL", info.countryCode)

	_, err = parseOutboundExternalInfo([]byte("loc=NL\n"))
	require.Error(t, err)
}

func TestExternalInfoFallsBackToSecondSource(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: externalInfoRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if strings.Contains(request.URL.Host, "primary") {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("198.51.100.8"))}, nil
	})}
	sources := []outboundExternalInfoSource{
		{name: "primary", endpoint: "https://primary.invalid", parse: parseOutboundExternalInfo},
		{name: "fallback", endpoint: "https://fallback.invalid", parse: parsePlainExternalIP},
	}
	info, err := fetchOutboundExternalInfoFromSources(context.Background(), client, sources)
	require.NoError(t, err)
	require.Equal(t, "198.51.100.8", info.ip)
	require.Equal(t, int32(2), calls.Load())
}

func TestExternalInfoCacheFreshAndStaleWindows(t *testing.T) {
	resolver := newOutboundExternalInfoResolver()
	now := time.Now()
	resolver.store("proxy", outboundExternalInfo{ip: "192.0.2.1", countryCode: "FI"}, now)

	fresh, loaded := resolver.load("proxy", now.Add(externalInfoCacheTTL-time.Millisecond), false)
	require.True(t, loaded)
	require.Equal(t, "FI", fresh.countryCode)
	_, loaded = resolver.load("proxy", now.Add(externalInfoCacheTTL+time.Millisecond), false)
	require.False(t, loaded)
	stale, loaded := resolver.load("proxy", now.Add(externalInfoCacheTTL+time.Millisecond), true)
	require.True(t, loaded)
	require.Equal(t, "192.0.2.1", stale.ip)
	_, loaded = resolver.load("proxy", now.Add(externalInfoStaleTTL), true)
	require.False(t, loaded)
}
