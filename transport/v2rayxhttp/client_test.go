package v2rayxhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func TestXmuxPoolIsBoundedAndWaitsForCapacity(t *testing.T) {
	t.Parallel()

	manager := newXmuxManager(&option.V2RayXHTTPXmuxConfig{
		MaxConcurrency: &option.V2RayXHTTPRangeConfig{From: 1, To: 1},
	}, func() *http.Client {
		return &http.Client{Transport: rejectingRoundTripper{}}
	})

	leases := make([]*xmuxLease, 0, defaultXmuxPoolLimit)
	for index := 0; index < defaultXmuxPoolLimit; index++ {
		lease, err := manager.acquire(context.Background())
		require.NoError(t, err)
		leases = append(leases, lease)
	}
	manager.access.Lock()
	require.Len(t, manager.clients, defaultXmuxPoolLimit)
	manager.access.Unlock()

	waitContext, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := manager.acquire(waitContext)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	leases[0].release()
	lease, err := manager.acquire(context.Background())
	require.NoError(t, err)
	lease.release()
	for index := 1; index < len(leases); index++ {
		leases[index].release()
	}
	manager.closeAll()
}

func TestXmuxConfiguredPoolIsClamped(t *testing.T) {
	t.Parallel()

	manager := newXmuxManager(&option.V2RayXHTTPXmuxConfig{
		MaxConnections: &option.V2RayXHTTPRangeConfig{From: 100, To: 100},
	}, func() *http.Client {
		return &http.Client{Transport: rejectingRoundTripper{}}
	})
	require.Equal(t, hardXmuxPoolLimit, manager.maxConnections)
	require.Equal(t, hardXmuxPoolLimit, manager.desiredConnections)
	manager.closeAll()
}

func TestXmuxZeroConfigurationUsesBoundedDefaults(t *testing.T) {
	t.Parallel()

	manager := newXmuxManager(&option.V2RayXHTTPXmuxConfig{}, func() *http.Client {
		return &http.Client{Transport: rejectingRoundTripper{}}
	})
	require.True(t, manager.useSafeDefaults)
	require.GreaterOrEqual(t, manager.concurrency, int32(16))
	require.LessOrEqual(t, manager.concurrency, int32(32))
	require.Equal(t, hardXmuxPoolLimit, manager.maxConnections)

	lease, err := manager.acquire(context.Background())
	require.NoError(t, err)
	manager.access.Lock()
	require.GreaterOrEqual(t, lease.client.leftRequests, int32(600))
	require.LessOrEqual(t, lease.client.leftRequests, int32(900))
	require.False(t, lease.client.unreusableAt.IsZero())
	manager.access.Unlock()
	lease.release()
	manager.closeAll()
}

func TestAutoModeSelection(t *testing.T) {
	t.Parallel()

	require.Equal(t, "packet-up", resolveMode("auto", false, false))
	require.Equal(t, "stream-up", resolveMode("auto", true, false))
	require.Equal(t, "stream-one", resolveMode("auto", true, true))
	require.Equal(t, "packet-up", resolveMode("packet-up", true, true))
}

func TestHTTP2KeepAlivePeriodIsBounded(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultH2ReadIdle, (&Client{}).http2ReadIdleTimeout())
	require.Equal(t, time.Duration(0), (&Client{options: option.V2RayXHTTPOptions{
		Xmux: &option.V2RayXHTTPXmuxConfig{HKeepAlivePeriod: -1},
	}}).http2ReadIdleTimeout())
	require.Equal(t, minimumH2ReadIdle, (&Client{options: option.V2RayXHTTPOptions{
		Xmux: &option.V2RayXHTTPXmuxConfig{HKeepAlivePeriod: 1},
	}}).http2ReadIdleTimeout())
	require.Equal(t, maximumH2ReadIdle, (&Client{options: option.V2RayXHTTPOptions{
		Xmux: &option.V2RayXHTTPXmuxConfig{HKeepAlivePeriod: 1 << 62},
	}}).http2ReadIdleTimeout())
}

func TestXmuxCloseUnblocksWaiter(t *testing.T) {
	t.Parallel()

	manager := newXmuxManager(&option.V2RayXHTTPXmuxConfig{
		MaxConcurrency: &option.V2RayXHTTPRangeConfig{From: 1, To: 1},
		MaxConnections: &option.V2RayXHTTPRangeConfig{From: 1, To: 1},
	}, func() *http.Client {
		return &http.Client{Transport: rejectingRoundTripper{}}
	})
	lease, err := manager.acquire(context.Background())
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, acquireErr := manager.acquire(context.Background())
		result <- acquireErr
	}()
	manager.closeAll()
	require.ErrorIs(t, <-result, net.ErrClosed)
	lease.release()
}

func TestXmuxCloseAllReleasesEveryTransportOnce(t *testing.T) {
	t.Parallel()

	var created atomic.Int32
	var closed atomic.Int32
	manager := newXmuxManager(&option.V2RayXHTTPXmuxConfig{
		MaxConcurrency: &option.V2RayXHTTPRangeConfig{From: 1, To: 1},
		MaxConnections: &option.V2RayXHTTPRangeConfig{From: 4, To: 4},
	}, func() *http.Client {
		created.Add(1)
		return &http.Client{Transport: &closeTrackingRoundTripper{closed: &closed}}
	})

	leases := make([]*xmuxLease, 0, 4)
	for range 4 {
		lease, err := manager.acquire(context.Background())
		require.NoError(t, err)
		leases = append(leases, lease)
	}
	require.Equal(t, int32(4), created.Load())

	manager.closeAll()
	manager.closeAll()
	require.Equal(t, int32(4), closed.Load())
	manager.access.Lock()
	require.Empty(t, manager.clients)
	manager.access.Unlock()
	_, err := manager.acquire(context.Background())
	require.ErrorIs(t, err, net.ErrClosed)

	for _, lease := range leases {
		lease.release()
		lease.release()
	}
	require.Equal(t, int32(4), closed.Load())
}

func TestPacketUpRoundTripAndClientClose(t *testing.T) {
	t.Parallel()

	uploads := make(chan []byte, 1)
	downloadStarted := make(chan struct{})
	downloadStopped := make(chan struct{})
	var startOnce sync.Once
	var stopOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			startOnce.Do(func() { close(downloadStarted) })
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			select {
			case payload := <-uploads:
				_, _ = writer.Write(payload)
				writer.(http.Flusher).Flush()
			case <-request.Context().Done():
				stopOnce.Do(func() { close(downloadStopped) })
				return
			}
			<-request.Context().Done()
			stopOnce.Do(func() { close(downloadStopped) })
		case http.MethodPost:
			payload, err := io.ReadAll(request.Body)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			uploads <- payload
			writer.WriteHeader(http.StatusOK)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client, err := NewClient(context.Background(), N.SystemDialer, M.ParseSocksaddr(parsedURL.Host), option.V2RayXHTTPOptions{
		Path: "/xhttp",
		Mode: "packet-up",
		ScMaxEachPostBytes: &option.V2RayXHTTPRangeConfig{
			From: 1024,
			To:   1024,
		},
		ScMinPostsIntervalMs: &option.V2RayXHTTPRangeConfig{
			From: 1,
			To:   1,
		},
	}, nil)
	require.NoError(t, err)

	conn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	select {
	case <-downloadStarted:
	case <-time.After(time.Second):
		t.Fatal("download request did not start")
	}
	payload := []byte("etonify-xhttp")
	_, err = conn.Write(payload)
	require.NoError(t, err)

	readResult := make(chan []byte, 1)
	readError := make(chan error, 1)
	go func() {
		buffer := make([]byte, len(payload))
		_, readErr := io.ReadFull(conn, buffer)
		if readErr != nil {
			readError <- readErr
			return
		}
		readResult <- buffer
	}()
	select {
	case received := <-readResult:
		require.Equal(t, payload, received)
	case readErr := <-readError:
		t.Fatal(readErr)
	case <-time.After(2 * time.Second):
		t.Fatal("xhttp round trip timed out")
	}

	require.NoError(t, client.Close())
	select {
	case <-downloadStopped:
	case <-time.After(time.Second):
		t.Fatal("active download was not cancelled by Client.Close")
	}
	_, err = client.DialContext(context.Background())
	require.ErrorIs(t, err, net.ErrClosed)
}

type rejectingRoundTripper struct{}

func (rejectingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network access is not expected")
}

type closeTrackingRoundTripper struct {
	closed *atomic.Int32
}

func (*closeTrackingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network access is not expected")
}

func (t *closeTrackingRoundTripper) CloseIdleConnections() {
	t.closed.Add(1)
}
