package v2rayxhttp

import (
	"bufio"
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

	"github.com/sagernet/sing-box/common/probe"
	U "github.com/sagernet/sing-box/common/urltest"
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
	require.Zero(t, manager.concurrency)
	require.Equal(t, 3, manager.desiredConnections)
	require.Equal(t, 3, manager.maxConnections)

	leasing := make([]*xmuxLease, 0, 3)
	for range 3 {
		lease, err := manager.acquire(context.Background())
		require.NoError(t, err)
		leasing = append(leasing, lease)
	}
	manager.access.Lock()
	require.Len(t, manager.clients, 3)
	for _, client := range manager.clients {
		require.GreaterOrEqual(t, client.leftRequests, int32(600))
		require.LessOrEqual(t, client.leftRequests, int32(900))
		require.False(t, client.unreusableAt.IsZero())
	}
	manager.access.Unlock()
	for index := range leasing {
		leasing[index].release()
	}
	manager.closeAll()
}

func TestAutoModeSelection(t *testing.T) {
	t.Parallel()

	require.Equal(t, "packet-up", resolveMode("auto", false))
	require.Equal(t, "stream-one", resolveMode("auto", true))
	require.Equal(t, "packet-up", resolveMode("packet-up", true))
	require.Equal(t, []string{"packet-up"}, resolveModeCandidates(false, false))
	require.Equal(t, []string{"packet-up", "stream-one", "stream-up"}, resolveModeCandidates(true, false))
	require.Equal(t, []string{"stream-one", "packet-up", "stream-up"}, resolveModeCandidates(true, true))
}

func TestURLTestAutoModeFallsBackAndCachesSuccessfulCandidate(t *testing.T) {
	t.Parallel()

	var packetRequests atomic.Int32
	var streamRequests atomic.Int32
	streamStarted := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			packetRequests.Add(1)
			writer.WriteHeader(http.StatusConflict)
		case http.MethodPost:
			streamRequests.Add(1)
			_ = http.NewResponseController(writer).EnableFullDuplex()
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			streamStarted <- struct{}{}
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "auto"})
	client.http2 = true
	probeSession := probe.NewURLTestSession()
	probeContext := probe.WithURLTest(context.Background(), probeSession)
	conn, err := client.DialContext(probeContext)
	require.Nil(t, conn)
	require.Error(t, err)
	require.True(t, probeSession.Next(err))

	conn, err = client.DialContext(probeContext)
	require.NoError(t, err)
	<-streamStarted
	require.NoError(t, conn.Close())
	probeSession.Success()
	require.Equal(t, int32(1), packetRequests.Load())
	require.Equal(t, int32(1), streamRequests.Load())

	cachedConn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	<-streamStarted
	require.NoError(t, cachedConn.Close())
	require.Equal(t, int32(1), packetRequests.Load())
	require.Equal(t, int32(2), streamRequests.Load())
	require.NoError(t, client.Close())
}

type xhttpURLTestDialer struct {
	client *Client
}

func (d xhttpURLTestDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.client.DialContext(ctx)
}

func (d xhttpURLTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("packet listening is not supported")
}

func TestURLTestRetriesCompleteXHTTPProbeBeforeCachingMode(t *testing.T) {
	t.Parallel()

	var packetRequests atomic.Int32
	var streamRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			packetRequests.Add(1)
			writer.WriteHeader(http.StatusConflict)
		case http.MethodPost:
			streamRequests.Add(1)
			_ = http.NewResponseController(writer).EnableFullDuplex()
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			tunneledRequest, err := http.ReadRequest(bufio.NewReader(request.Body))
			if err != nil {
				return
			}
			_ = tunneledRequest.Body.Close()
			_, _ = writer.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
			writer.(http.Flusher).Flush()
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "auto"})
	client.http2 = true
	delay, err := U.URLTest(
		context.Background(),
		"http://example.com/generate_204",
		xhttpURLTestDialer{client: client},
	)
	require.NoError(t, err)
	require.LessOrEqual(t, delay, uint16(1000))
	require.Equal(t, int32(1), packetRequests.Load())
	require.Equal(t, int32(1), streamRequests.Load())

	cachedConn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	require.NoError(t, cachedConn.Close())
	require.Equal(t, int32(1), packetRequests.Load())
	require.NoError(t, client.Close())
}

func TestAutoModeFallbackIsLimitedToURLTest(t *testing.T) {
	t.Parallel()

	var streamRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			streamRequests.Add(1)
		}
		writer.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "auto"})
	client.http2 = true
	conn, err := client.DialContext(context.Background())
	require.Nil(t, conn)
	require.Error(t, err)
	require.Zero(t, streamRequests.Load())
	require.NoError(t, client.Close())
}

func TestExplicitModeNeverFallsBackDuringURLTest(t *testing.T) {
	t.Parallel()

	var streamRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			streamRequests.Add(1)
		}
		writer.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "packet-up"})
	client.http2 = true
	probeSession := probe.NewURLTestSession()
	conn, err := client.DialContext(probe.WithURLTest(context.Background(), probeSession))
	require.Nil(t, conn)
	require.Error(t, err)
	require.Zero(t, streamRequests.Load())
	require.NoError(t, client.Close())
}

func TestAutoModeCacheIsInvalidatedByReset(t *testing.T) {
	t.Parallel()

	var packetRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			packetRequests.Add(1)
			writer.WriteHeader(http.StatusConflict)
			return
		}
		_ = http.NewResponseController(writer).EnableFullDuplex()
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "auto"})
	client.http2 = true
	probeSession := probe.NewURLTestSession()
	probeContext := probe.WithURLTest(context.Background(), probeSession)
	conn, err := client.DialContext(probeContext)
	require.Nil(t, conn)
	require.Error(t, err)
	require.True(t, probeSession.Next(err))
	conn, err = client.DialContext(probeContext)
	require.NoError(t, err)
	probeSession.Success()
	require.NoError(t, conn.Close())

	client.Reset()
	conn, err = client.DialContext(context.Background())
	require.Nil(t, conn)
	require.Error(t, err)
	require.Equal(t, int32(2), packetRequests.Load())
	require.NoError(t, client.Close())
}

func TestConcurrentURLTestsShareAutoModeDiscovery(t *testing.T) {
	t.Parallel()

	var packetRequests atomic.Int32
	packetStarted := make(chan struct{})
	releasePacket := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			if packetRequests.Add(1) == 1 {
				close(packetStarted)
			}
			<-releasePacket
			writer.WriteHeader(http.StatusConflict)
			return
		}
		_ = http.NewResponseController(writer).EnableFullDuplex()
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "auto"})
	client.http2 = true
	ownerSession := probe.NewURLTestSession()
	ownerContext := probe.WithURLTest(context.Background(), ownerSession)
	followerSession := probe.NewURLTestSession()
	followerContext, cancelFollower := context.WithTimeout(
		probe.WithURLTest(context.Background(), followerSession),
		5*time.Second,
	)
	defer cancelFollower()

	type dialResult struct {
		conn net.Conn
		err  error
	}
	ownerResult := make(chan dialResult, 1)
	go func() {
		conn, err := client.DialContext(ownerContext)
		ownerResult <- dialResult{conn: conn, err: err}
	}()
	<-packetStarted
	followerResult := make(chan dialResult, 1)
	go func() {
		conn, err := client.DialContext(followerContext)
		followerResult <- dialResult{conn: conn, err: err}
	}()
	require.Eventually(t, func() bool {
		return packetRequests.Load() == 1
	}, time.Second, 10*time.Millisecond)
	close(releasePacket)

	first := <-ownerResult
	require.Nil(t, first.conn)
	require.Error(t, first.err)
	require.True(t, ownerSession.Next(first.err))
	ownerConn, err := client.DialContext(ownerContext)
	require.NoError(t, err)
	ownerSession.Success()
	require.NoError(t, ownerConn.Close())

	follower := <-followerResult
	require.NoError(t, follower.err)
	require.NotNil(t, follower.conn)
	require.NoError(t, follower.conn.Close())
	require.Equal(t, int32(1), packetRequests.Load())
	require.NoError(t, client.Close())
}

func TestModeCompatibilityErrorClassification(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		err        error
		compatible bool
	}{
		{name: "bad request", err: &xhttpTransportError{statusCode: http.StatusBadRequest}, compatible: true},
		{name: "not found", err: &xhttpTransportError{statusCode: http.StatusNotFound}, compatible: false},
		{name: "conflict", err: &xhttpTransportError{statusCode: http.StatusConflict}, compatible: true},
		{name: "misdirected", err: &xhttpTransportError{statusCode: http.StatusMisdirectedRequest}, compatible: true},
		{name: "server error", err: &xhttpTransportError{statusCode: http.StatusBadGateway}, compatible: false},
		{name: "xhttp early eof", err: &xhttpTransportError{phase: "response_stream", cause: io.ErrUnexpectedEOF}, compatible: true},
		{name: "bare early eof", err: io.ErrUnexpectedEOF, compatible: false},
		{name: "xhttp response body closed", err: &xhttpTransportError{phase: "response_stream", cause: errors.New("http2: response body closed")}, compatible: true},
		{name: "bare response body closed", err: errors.New("http2: response body closed"), compatible: false},
		{name: "timeout", err: context.DeadlineExceeded, compatible: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.compatible, isModeCompatibilityError(testCase.err))
		})
	}
}

func TestXHTTPTransportErrorRedactsRequestURL(t *testing.T) {
	t.Parallel()

	err := &xhttpTransportError{
		phase:  "response_stream",
		reason: "request_failed",
		cause: &url.Error{
			Op:  "Get",
			URL: "https://example.com/private/path?token=secret",
			Err: io.ErrUnexpectedEOF,
		},
	}
	require.NotContains(t, err.Error(), "private")
	require.NotContains(t, err.Error(), "secret")
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestDialContextWaitsForDownloadResponse(t *testing.T) {
	t.Parallel()

	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-releaseResponse
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "packet-up"})
	dialResult := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := client.DialContext(context.Background())
		dialResult <- struct {
			conn net.Conn
			err  error
		}{conn: conn, err: err}
	}()

	<-requestStarted
	var earlyResult *struct {
		conn net.Conn
		err  error
	}
	select {
	case result := <-dialResult:
		earlyResult = &result
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseResponse)
	if earlyResult != nil {
		if earlyResult.conn != nil {
			_ = earlyResult.conn.Close()
		}
		_ = client.Close()
		t.Fatal("DialContext returned before the download response was ready")
	}
	result := <-dialResult
	require.NoError(t, result.err)
	require.NotNil(t, result.conn)
	require.NoError(t, result.conn.Close())
	require.NoError(t, client.Close())
}

func TestDialContextReturnsDownloadStatusFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "packet-up"})
	conn, err := client.DialContext(context.Background())
	require.Nil(t, conn)
	require.ErrorContains(t, err, "unexpected stream-down status: 404 Not Found")
	require.NoError(t, client.Close())
}

func TestCallerCancellationAfterDialDoesNotCancelSession(t *testing.T) {
	t.Parallel()

	const payload = "session-survived"
	responseReady := make(chan struct{})
	sendPayload := make(chan struct{})
	serverCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		close(responseReady)
		select {
		case <-sendPayload:
			_, _ = io.WriteString(writer, payload)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		case <-request.Context().Done():
			close(serverCanceled)
		}
	}))
	defer server.Close()

	client := newPlainTestClient(t, server.URL, option.V2RayXHTTPOptions{Mode: "packet-up"})
	dialContext, cancelDial := context.WithCancel(context.Background())
	conn, err := client.DialContext(dialContext)
	require.NoError(t, err)
	<-responseReady
	cancelDial()

	select {
	case <-serverCanceled:
		_ = conn.Close()
		t.Fatal("caller cancellation terminated an established XHTTP session")
	case <-time.After(50 * time.Millisecond):
	}
	close(sendPayload)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	received := make([]byte, len(payload))
	_, err = io.ReadFull(conn, received)
	require.NoError(t, err)
	require.Equal(t, payload, string(received))
	require.NoError(t, conn.Close())
	require.NoError(t, client.Close())
}

func newPlainTestClient(t *testing.T, rawURL string, options option.V2RayXHTTPOptions) *Client {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	require.NoError(t, err)
	client, err := NewClient(
		context.Background(),
		N.SystemDialer,
		M.ParseSocksaddr(parsedURL.Host),
		options,
		nil,
	)
	require.NoError(t, err)
	return client
}

func TestHTTP2KeepAlivePeriodMatchesReferencePolicy(t *testing.T) {
	t.Parallel()

	require.Equal(t, 45*time.Second, (&Client{}).http2ReadIdleTimeout())
	require.Equal(t, time.Duration(0), (&Client{options: option.V2RayXHTTPOptions{
		Xmux: &option.V2RayXHTTPXmuxConfig{HKeepAlivePeriod: -1},
	}}).http2ReadIdleTimeout())
	require.Equal(t, time.Second, (&Client{options: option.V2RayXHTTPOptions{
		Xmux: &option.V2RayXHTTPXmuxConfig{HKeepAlivePeriod: 1},
	}}).http2ReadIdleTimeout())
}

func TestClientWithoutXmuxUsesReferenceDefaultPool(t *testing.T) {
	t.Parallel()

	client := newPlainTestClient(t, "http://127.0.0.1:1", option.V2RayXHTTPOptions{})
	leasing := make([]*httpClientLease, 0, 4)
	for range 4 {
		lease, err := client.getHTTPClient(context.Background())
		require.NoError(t, err)
		require.False(t, lease.owned)
		leasing = append(leasing, lease)
	}
	client.xmuxAccess.Lock()
	require.NotNil(t, client.xmuxManager)
	require.Len(t, client.xmuxManager.clients, 3)
	client.xmuxAccess.Unlock()
	for index := range leasing {
		leasing[index].Close()
	}
	require.NoError(t, client.Close())
}

func TestClientRejectsConflictingXmuxLimits(t *testing.T) {
	t.Parallel()

	_, err := NewClient(
		context.Background(),
		N.SystemDialer,
		M.ParseSocksaddr("127.0.0.1:443"),
		option.V2RayXHTTPOptions{Xmux: &option.V2RayXHTTPXmuxConfig{
			MaxConcurrency: &option.V2RayXHTTPRangeConfig{From: 1, To: 1},
			MaxConnections: &option.V2RayXHTTPRangeConfig{From: 2, To: 2},
		}},
		nil,
	)
	require.ErrorContains(t, err, "max_connections cannot be used with max_concurrency")
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

func TestClientResetCancelsActiveSessionAndKeepsClientReusable(t *testing.T) {
	t.Parallel()

	downloadStarted := make(chan struct{}, 2)
	downloadStopped := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			downloadStarted <- struct{}{}
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			downloadStopped <- struct{}{}
		case http.MethodPost:
			_, _ = io.Copy(io.Discard, request.Body)
			writer.WriteHeader(http.StatusOK)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client, err := NewClient(
		context.Background(),
		N.SystemDialer,
		M.ParseSocksaddr(parsedURL.Host),
		option.V2RayXHTTPOptions{Path: "/xhttp", Mode: "packet-up"},
		nil,
	)
	require.NoError(t, err)
	defer client.Close()

	firstConn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	select {
	case <-downloadStarted:
	case <-time.After(time.Second):
		t.Fatal("first download request did not start")
	}

	client.Reset()
	select {
	case <-downloadStopped:
	case <-time.After(time.Second):
		t.Fatal("reset did not cancel the active download")
	}
	_, err = firstConn.Write([]byte("stale"))
	require.Error(t, err)

	secondConn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	select {
	case <-downloadStarted:
	case <-time.After(time.Second):
		t.Fatal("download request did not restart after reset")
	}
	require.NoError(t, secondConn.Close())
}

func TestClientResetReplacesXmuxPool(t *testing.T) {
	t.Parallel()

	client, err := NewClient(
		context.Background(),
		N.SystemDialer,
		M.ParseSocksaddr("127.0.0.1:80"),
		option.V2RayXHTTPOptions{
			Path: "/xhttp",
			Mode: "packet-up",
			Xmux: &option.V2RayXHTTPXmuxConfig{
				MaxConnections: &option.V2RayXHTTPRangeConfig{From: 1, To: 1},
			},
		},
		nil,
	)
	require.NoError(t, err)
	defer client.Close()

	firstLease, err := client.getHTTPClient(context.Background())
	require.NoError(t, err)
	firstManager := client.xmuxManager
	require.NotNil(t, firstManager)
	firstLease.Close()

	client.Reset()
	firstManager.access.Lock()
	require.True(t, firstManager.closed)
	firstManager.access.Unlock()
	require.Nil(t, client.xmuxManager)

	secondLease, err := client.getHTTPClient(context.Background())
	require.NoError(t, err)
	require.NotSame(t, firstManager, client.xmuxManager)
	secondLease.Close()
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
