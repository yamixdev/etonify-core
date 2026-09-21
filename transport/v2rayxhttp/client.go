package v2rayxhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/probe"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	sHTTP "github.com/sagernet/sing/protocol/http"

	"golang.org/x/net/http2"
)

const (
	defaultXmuxPoolLimit = 16
	hardXmuxPoolLimit    = 16
	maxPacketUploadBytes = 256 * 1024
	packetUploadTimeout  = 30 * time.Second
	establishmentTimeout = 15 * time.Second
	defaultHTTPIdle      = 300 * time.Second
	defaultH2ReadIdle    = 45 * time.Second
)

var _ adapter.V2RayClientTransport = (*Client)(nil)
var _ adapter.V2RayClientTransportResetter = (*Client)(nil)

type Client struct {
	dialer     N.Dialer
	serverAddr M.Socksaddr
	tlsConfig  tls.Config
	config     *xhttpConfig
	options    option.V2RayXHTTPOptions
	requestURL url.URL
	http2      bool
	reality    bool

	lifecycleContext context.Context
	lifecycleCancel  context.CancelFunc
	stateAccess      sync.Mutex
	closed           bool
	generation       uint64
	sessions         map[*splitConn]struct{}
	resolvedMode     string
	resolvedModeGen  uint64
	modeProbe        *modeProbeState
	closeOnce        sync.Once
	closeDone        chan struct{}
	closeErr         error

	xmuxAccess  sync.Mutex
	xmuxManager *xmuxManager
}

type modeProbeState struct {
	generation uint64
	done       chan struct{}
	once       sync.Once
	mode       string
	err        error
}

type modeTrial struct {
	access     sync.Mutex
	client     *Client
	state      *modeProbeState
	candidates []string
	index      int
	complete   bool
}

func (s *modeProbeState) complete(mode string, err error) {
	s.once.Do(func() {
		s.mode = mode
		s.err = err
		close(s.done)
	})
}

func (t *modeTrial) current() (string, bool) {
	t.access.Lock()
	defer t.access.Unlock()
	if t.complete || t.index >= len(t.candidates) {
		return "", false
	}
	t.client.stateAccess.Lock()
	valid := !t.client.closed && t.state.generation == t.client.generation
	t.client.stateAccess.Unlock()
	return t.candidates[t.index], valid
}

func (t *modeTrial) Next(err error) bool {
	t.access.Lock()
	defer t.access.Unlock()
	if t.complete || !isModeCompatibilityError(err) || t.index+1 >= len(t.candidates) {
		return false
	}
	t.index++
	return true
}

func (t *modeTrial) Success() {
	t.access.Lock()
	if t.complete || t.index >= len(t.candidates) {
		t.access.Unlock()
		return
	}
	t.complete = true
	mode := t.candidates[t.index]
	t.access.Unlock()
	t.client.finishModeProbe(t.state, mode, nil)
}

func (t *modeTrial) Failure(err error) {
	t.access.Lock()
	if t.complete {
		t.access.Unlock()
		return
	}
	t.complete = true
	t.access.Unlock()
	t.client.finishModeProbe(t.state, "", err)
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayXHTTPOptions, tlsConfig tls.Config) (*Client, error) {
	if options.Xmux != nil &&
		rangeHasPositiveValue(options.Xmux.MaxConnections) &&
		rangeHasPositiveValue(options.Xmux.MaxConcurrency) {
		return nil, E.New("xhttp: xmux max_connections cannot be used with max_concurrency")
	}
	config := newConfig(options)
	if err := config.validate(); err != nil {
		return nil, err
	}
	switch config.mode {
	case "auto", "packet-up", "stream-up", "stream-one":
	default:
		return nil, E.New("unsupported xhttp mode: ", config.mode)
	}

	var requestURL url.URL
	if tlsConfig == nil {
		requestURL.Scheme = "http"
	} else {
		requestURL.Scheme = "https"
	}
	requestURL.Host = serverAddr.String()
	if err := sHTTP.URLSetPath(&requestURL, config.path); err != nil {
		return nil, E.Cause(err, "parse path")
	}
	if config.query != "" {
		requestURL.RawQuery = config.query
	}

	if config.host == "" {
		if tlsConfig != nil && tlsConfig.ServerName() != "" {
			config.host = tlsConfig.ServerName()
		} else {
			config.host = serverAddr.AddrString()
		}
	}
	if tlsConfig != nil && len(tlsConfig.NextProtos()) == 0 {
		tlsConfig.SetNextProtos([]string{http2.NextProtoTLS})
	}

	lifecycleContext, lifecycleCancel := context.WithCancel(ctx)
	_, isReality := tlsConfig.(interface{ IsReality() bool })
	return &Client{
		dialer:           dialer,
		serverAddr:       serverAddr,
		tlsConfig:        tlsConfig,
		config:           config,
		options:          options,
		requestURL:       requestURL,
		http2:            tlsConfig != nil,
		reality:          isReality,
		lifecycleContext: lifecycleContext,
		lifecycleCancel:  lifecycleCancel,
		sessions:         make(map[*splitConn]struct{}),
		closeDone:        make(chan struct{}),
	}, nil
}

func (c *Client) createHTTPClient() *http.Client {
	var transport http.RoundTripper
	if c.tlsConfig != nil {
		tlsDialer := tls.NewDialer(c.dialer, c.tlsConfig)
		transport = &http2.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string, config *tls.STDConfig) (net.Conn, error) {
				return tlsDialer.DialTLSContext(ctx, M.ParseSocksaddr(addr))
			},
			IdleConnTimeout: defaultHTTPIdle,
			ReadIdleTimeout: c.http2ReadIdleTimeout(),
		}
	} else {
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return c.dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
			},
			IdleConnTimeout:   defaultHTTPIdle,
			DisableKeepAlives: true,
		}
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (c *Client) http2ReadIdleTimeout() time.Duration {
	if c.options.Xmux == nil || c.options.Xmux.HKeepAlivePeriod == 0 {
		return defaultH2ReadIdle
	}
	if c.options.Xmux.HKeepAlivePeriod < 0 {
		return 0
	}
	return time.Duration(c.options.Xmux.HKeepAlivePeriod) * time.Second
}

func (c *Client) getHTTPClient(ctx context.Context) (*httpClientLease, error) {
	c.xmuxAccess.Lock()
	if c.xmuxManager == nil {
		xmuxConfig := c.options.Xmux
		if xmuxConfig == nil {
			xmuxConfig = &option.V2RayXHTTPXmuxConfig{}
		}
		c.xmuxManager = newXmuxManager(xmuxConfig, c.createHTTPClient)
	}
	manager := c.xmuxManager
	c.xmuxAccess.Unlock()

	xmuxLease, err := manager.acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &httpClientLease{client: xmuxLease.client.httpClient, xmux: xmuxLease}, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	requestedMode := c.config.mode
	if requestedMode != "" && requestedMode != "auto" {
		return c.dialModeContext(ctx, requestedMode)
	}
	if cachedMode := c.loadResolvedMode(); cachedMode != "" {
		return c.dialModeContext(ctx, cachedMode)
	}
	candidates := resolveModeCandidates(c.http2, c.reality)
	probeSession := probe.URLTestSessionFromContext(ctx)
	if probeSession == nil || len(candidates) == 1 {
		return c.dialModeContext(ctx, candidates[0])
	}
	if controller := probeSession.Controller(); controller != nil {
		trial, isModeTrial := controller.(*modeTrial)
		if !isModeTrial || trial.client != c {
			return c.dialModeContext(ctx, candidates[0])
		}
		candidate, valid := trial.current()
		if !valid {
			return nil, net.ErrClosed
		}
		return c.dialModeContext(ctx, candidate)
	}

	probeState, owner, cachedMode, err := c.beginModeProbe()
	if err != nil {
		return nil, err
	}
	if cachedMode != "" {
		return c.dialModeContext(ctx, cachedMode)
	}
	if !owner {
		select {
		case <-probeState.done:
			if probeState.mode == "" {
				return nil, probeState.err
			}
			return c.dialModeContext(ctx, probeState.mode)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	trial := &modeTrial{
		client:     c,
		state:      probeState,
		candidates: candidates,
	}
	if !probeSession.BindController(trial) {
		err = E.New("xhttp: URLTest compatibility controller already bound")
		c.finishModeProbe(probeState, "", err)
		return nil, err
	}
	return c.dialModeContext(ctx, candidates[0])
}

func (c *Client) dialModeContext(ctx context.Context, mode string) (net.Conn, error) {
	if c.lifecycleContext.Err() != nil {
		return nil, net.ErrClosed
	}
	generation, available := c.beginDial()
	if !available {
		return nil, net.ErrClosed
	}
	requestContext, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	stopLifecycleCancel := context.AfterFunc(c.lifecycleContext, cancelRequests)
	establishmentContext, cancelEstablishment := context.WithTimeout(requestContext, establishmentTimeout)
	stopCallerCancel := context.AfterFunc(ctx, cancelEstablishment)
	defer func() {
		stopCallerCancel()
		cancelEstablishment()
	}()

	httpLease, err := c.getHTTPClient(establishmentContext)
	if err != nil {
		stopLifecycleCancel()
		cancelRequests()
		return nil, err
	}

	var sessionID string
	if mode != "stream-one" {
		sessionID, err = c.config.generateSessionID()
		if err != nil {
			httpLease.Close()
			stopLifecycleCancel()
			cancelRequests()
			return nil, E.Cause(err, "generate session ID")
		}
	}

	uploadReader, uploadWriter := io.Pipe()
	downloadReader := newWaitReadCloser()
	conn := &splitConn{
		writer:     uploadWriter,
		reader:     downloadReader,
		remoteAddr: c.serverAddr,
	}
	conn.onClose = func() {
		stopLifecycleCancel()
		cancelRequests()
		_ = uploadReader.CloseWithError(io.ErrClosedPipe)
		httpLease.Close()
		c.unregister(conn)
	}
	if !c.register(conn, generation) {
		_ = conn.Close()
		return nil, net.ErrClosed
	}

	switch mode {
	case "stream-one":
		err = c.dialStreamOne(requestContext, establishmentContext, httpLease, downloadReader, uploadReader)
	case "stream-up":
		err = c.dialStreamUp(requestContext, httpLease, downloadReader, sessionID, uploadReader)
	default:
		err = c.dialPacketUp(requestContext, httpLease, downloadReader, sessionID, uploadReader)
	}
	if err == nil && mode != "stream-one" {
		err = downloadReader.Wait(establishmentContext)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c *Client) loadResolvedMode() string {
	c.stateAccess.Lock()
	defer c.stateAccess.Unlock()
	if c.closed || c.resolvedModeGen != c.generation {
		return ""
	}
	return c.resolvedMode
}

func (c *Client) beginModeProbe() (*modeProbeState, bool, string, error) {
	c.stateAccess.Lock()
	defer c.stateAccess.Unlock()
	if c.closed {
		return nil, false, "", net.ErrClosed
	}
	if c.resolvedMode != "" && c.resolvedModeGen == c.generation {
		return nil, false, c.resolvedMode, nil
	}
	if c.modeProbe != nil && c.modeProbe.generation == c.generation {
		return c.modeProbe, false, "", nil
	}
	state := &modeProbeState{
		generation: c.generation,
		done:       make(chan struct{}),
	}
	c.modeProbe = state
	return state, true, "", nil
}

func (c *Client) finishModeProbe(state *modeProbeState, mode string, err error) {
	c.stateAccess.Lock()
	if !c.closed && state.generation == c.generation && mode != "" {
		c.resolvedMode = mode
		c.resolvedModeGen = c.generation
	}
	if c.modeProbe == state {
		c.modeProbe = nil
	}
	c.stateAccess.Unlock()
	state.complete(mode, err)
}

func (c *Client) beginDial() (uint64, bool) {
	c.stateAccess.Lock()
	defer c.stateAccess.Unlock()
	if c.closed {
		return 0, false
	}
	return c.generation, true
}

func (c *Client) register(conn *splitConn, generation uint64) bool {
	c.stateAccess.Lock()
	defer c.stateAccess.Unlock()
	if c.closed || generation != c.generation {
		return false
	}
	c.sessions[conn] = struct{}{}
	return true
}

func (c *Client) unregister(conn *splitConn) {
	c.stateAccess.Lock()
	delete(c.sessions, conn)
	c.stateAccess.Unlock()
}

func (c *Client) buildRequest(ctx context.Context, method string, sessionID string, sequence string, body io.Reader) (*http.Request, error) {
	requestURL := c.requestURL
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, err
	}
	request.Host = c.config.host
	request.Header = c.config.headers.Clone()
	c.config.applyMetaToRequest(request, sessionID, sequence)
	c.config.applyXPaddingToRequest(request, requestURL.String())
	if body != nil && sequence == "" && method == c.config.uplinkHTTPMethod && !c.config.noGRPCHeader {
		request.Header.Set("Content-Type", "application/grpc")
	}
	return request, nil
}

func (c *Client) dialStreamOne(ctx context.Context, establishmentContext context.Context, lease *httpClientLease, download *waitReadCloser, upload *io.PipeReader) error {
	connected := make(chan struct{})
	var connectedOnce sync.Once
	traceContext := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			connectedOnce.Do(func() { close(connected) })
		},
	})
	request, err := c.buildRequest(traceContext, c.config.uplinkHTTPMethod, "", "", upload)
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	lease.consumeRequest()
	go func() {
		response, doErr := lease.client.Do(request)
		if doErr != nil {
			transportErr := newXHTTPTransportError("http_connection", "request_failed", doErr)
			_ = upload.CloseWithError(transportErr)
			download.Fail(transportErr)
			result <- transportErr
			return
		}
		if response.StatusCode != http.StatusOK {
			statusErr := unexpectedStatus("stream-one", response)
			_ = upload.CloseWithError(statusErr)
			download.Fail(statusErr)
			result <- statusErr
			return
		}
		download.Set(response.Body)
		result <- nil
	}()
	select {
	case <-connected:
		select {
		case resultErr := <-result:
			return resultErr
		default:
			return nil
		}
	case resultErr := <-result:
		return resultErr
	case <-establishmentContext.Done():
		return establishmentContext.Err()
	}
}

func (c *Client) dialStreamUp(ctx context.Context, lease *httpClientLease, download *waitReadCloser, sessionID string, upload *io.PipeReader) error {
	downloadRequest, err := c.buildRequest(ctx, http.MethodGet, sessionID, "", nil)
	if err != nil {
		return err
	}
	uploadRequest, err := c.buildRequest(ctx, c.config.uplinkHTTPMethod, sessionID, "", upload)
	if err != nil {
		return err
	}

	lease.consumeRequest()
	go c.runDownloadRequest(lease.client, downloadRequest, "stream-down", download, upload)
	lease.consumeRequest()
	go func() {
		response, doErr := lease.client.Do(uploadRequest)
		if doErr != nil {
			transportErr := newXHTTPTransportError("upload_stream", "request_failed", doErr)
			_ = upload.CloseWithError(transportErr)
			download.Fail(transportErr)
			return
		}
		if response.StatusCode != http.StatusOK {
			statusErr := unexpectedStatus("stream-up", response)
			_ = upload.CloseWithError(statusErr)
			download.Fail(statusErr)
			return
		}
		drainAndClose(response.Body)
	}()
	return nil
}

func (c *Client) dialPacketUp(ctx context.Context, lease *httpClientLease, download *waitReadCloser, sessionID string, upload *io.PipeReader) error {
	downloadRequest, err := c.buildRequest(ctx, http.MethodGet, sessionID, "", nil)
	if err != nil {
		return err
	}
	lease.consumeRequest()
	go c.runDownloadRequest(lease.client, downloadRequest, "stream-down", download, upload)

	go func() {
		maxUploadSize := int(randRange(c.config.scMaxEachPostBytesFrom, c.config.scMaxEachPostBytesTo))
		if maxUploadSize < 64 {
			maxUploadSize = 64
		}
		if maxUploadSize > maxPacketUploadBytes {
			maxUploadSize = maxPacketUploadBytes
		}
		buffer := make([]byte, maxUploadSize)
		var sequence int64
		for {
			n, readErr := upload.Read(buffer)
			if n > 0 {
				if err := c.sendPacketUpload(ctx, lease, sessionID, strconv.FormatInt(sequence, 10), buffer[:n]); err != nil {
					_ = upload.CloseWithError(err)
					download.Fail(err)
					return
				}
				sequence++
				if c.config.scMinPostsIntervalMsFrom > 0 {
					interval := randRange(c.config.scMinPostsIntervalMsFrom, c.config.scMinPostsIntervalMsTo)
					if !waitForContext(ctx, time.Duration(interval)*time.Millisecond) {
						return
					}
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	return nil
}

func (c *Client) runDownloadRequest(client *http.Client, request *http.Request, operation string, download *waitReadCloser, upload *io.PipeReader) {
	response, err := client.Do(request)
	if err != nil {
		transportErr := newXHTTPTransportError("response_stream", "request_failed", err)
		_ = upload.CloseWithError(transportErr)
		download.Fail(transportErr)
		return
	}
	if response.StatusCode != http.StatusOK {
		statusErr := unexpectedStatus(operation, response)
		_ = upload.CloseWithError(statusErr)
		download.Fail(statusErr)
		return
	}
	download.Set(response.Body)
}

func (c *Client) sendPacketUpload(ctx context.Context, lease *httpClientLease, sessionID string, sequence string, payload []byte) error {
	requestContext, cancel := context.WithTimeout(ctx, packetUploadTimeout)
	defer cancel()

	var body io.Reader
	if c.config.uplinkDataPlacement == PlacementBody || c.config.uplinkDataPlacement == PlacementAuto {
		body = bytes.NewReader(payload)
	}
	request, err := c.buildRequest(requestContext, c.config.uplinkHTTPMethod, sessionID, sequence, body)
	if err != nil {
		return err
	}
	if body != nil {
		request.ContentLength = int64(len(payload))
	} else {
		c.config.encodeUplinkData(request, payload)
	}

	lease.consumeRequest()
	response, err := lease.client.Do(request)
	if err != nil {
		return newXHTTPTransportError("upload_stream", "request_failed", err)
	}
	if response.StatusCode != http.StatusOK {
		return unexpectedStatus("packet-up", response)
	}
	drainAndClose(response.Body)
	return nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.close()
		close(c.closeDone)
	})
	<-c.closeDone
	return c.closeErr
}

// Reset drops every session and pooled HTTP transport that belongs to the
// previous physical network while keeping the XHTTP client reusable. A dial
// that started before the reset is rejected by the generation check in
// register, so a stale Wi-Fi transport cannot become active after handover.
func (c *Client) Reset() {
	c.stateAccess.Lock()
	if c.closed {
		c.stateAccess.Unlock()
		return
	}
	c.generation++
	c.resolvedMode = ""
	c.resolvedModeGen = 0
	modeProbe := c.modeProbe
	c.modeProbe = nil
	sessions := make([]*splitConn, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.xmuxAccess.Lock()
	manager := c.xmuxManager
	c.xmuxManager = nil
	c.xmuxAccess.Unlock()
	c.stateAccess.Unlock()
	if modeProbe != nil {
		modeProbe.complete("", net.ErrClosed)
	}

	for _, session := range sessions {
		_ = session.Close()
	}
	if manager != nil {
		manager.closeAll()
	}
}

func (c *Client) close() error {
	c.stateAccess.Lock()
	c.closed = true
	modeProbe := c.modeProbe
	c.modeProbe = nil
	sessions := make([]*splitConn, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.stateAccess.Unlock()
	if modeProbe != nil {
		modeProbe.complete("", net.ErrClosed)
	}

	c.lifecycleCancel()
	var closeErr error
	for _, session := range sessions {
		closeErr = errors.Join(closeErr, session.Close())
	}
	c.xmuxAccess.Lock()
	manager := c.xmuxManager
	c.xmuxAccess.Unlock()
	if manager != nil {
		manager.closeAll()
	}
	return closeErr
}

func resolveMode(mode string, realityEnabled bool) string {
	if mode != "" && mode != "auto" {
		return mode
	}
	return resolveModeCandidates(true, realityEnabled)[0]
}

func resolveModeCandidates(http2Enabled bool, realityEnabled bool) []string {
	if !http2Enabled {
		return []string{"packet-up"}
	}
	if realityEnabled {
		return []string{"stream-one", "packet-up", "stream-up"}
	}
	return []string{"packet-up", "stream-one", "stream-up"}
}

type xhttpTransportError struct {
	phase      string
	reason     string
	operation  string
	statusCode int
	cause      error
}

func newXHTTPTransportError(phase string, reason string, cause error) error {
	var existing *xhttpTransportError
	if errors.As(cause, &existing) {
		return existing
	}
	return &xhttpTransportError{
		phase:  phase,
		reason: reason,
		cause:  cause,
	}
}

func (e *xhttpTransportError) Error() string {
	if e.statusCode != 0 {
		return "xhttp: unexpected " + e.operation + " status: " +
			strconv.Itoa(e.statusCode) + " " + http.StatusText(e.statusCode)
	}
	if e.cause != nil {
		return "xhttp: " + e.phase + ": " + redactedTransportCause(e.cause)
	}
	return "xhttp: " + e.reason
}

func redactedTransportCause(err error) string {
	for {
		var urlError *url.Error
		if !errors.As(err, &urlError) || urlError.Err == nil {
			return err.Error()
		}
		err = urlError.Err
	}
}

func (e *xhttpTransportError) Unwrap() error {
	return e.cause
}

func (e *xhttpTransportError) URLTestErrorCode() string {
	if e.statusCode != 0 {
		return "xhttp_http_status"
	}
	if errors.Is(e.cause, io.ErrUnexpectedEOF) {
		return "xhttp_early_eof"
	}
	if e.cause != nil && strings.Contains(strings.ToLower(redactedTransportCause(e.cause)), "http2: response body closed") {
		return "xhttp_response_closed"
	}
	if e.phase != "" {
		return "xhttp_" + e.phase
	}
	return "xhttp_transport"
}

func (e *xhttpTransportError) URLTestErrorMessage() string {
	if e.statusCode != 0 {
		return "XHTTP " + e.operation + " rejected with HTTP " + strconv.Itoa(e.statusCode)
	}
	if e.phase != "" {
		return "XHTTP " + strings.ReplaceAll(e.phase, "_", " ") + " failed"
	}
	return "XHTTP transport failed"
}

func unexpectedStatus(operation string, response *http.Response) error {
	statusCode := response.StatusCode
	_ = response.Body.Close()
	return &xhttpTransportError{
		phase:      "http_status",
		reason:     "unexpected_http_status",
		operation:  operation,
		statusCode: statusCode,
	}
}

func isModeCompatibilityError(err error) bool {
	var transportErr *xhttpTransportError
	if !errors.As(err, &transportErr) {
		return false
	}
	switch transportErr.statusCode {
	case http.StatusBadRequest, http.StatusConflict, http.StatusMisdirectedRequest:
		return true
	}
	if transportErr.cause == nil {
		return false
	}
	if errors.Is(transportErr.cause, io.ErrUnexpectedEOF) {
		return true
	}
	message := strings.ToLower(redactedTransportCause(transportErr.cause))
	return strings.Contains(message, "http2: response body closed")
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.CopyN(io.Discard, body, 32*1024)
	_ = body.Close()
}

func waitForContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type httpClientLease struct {
	client    *http.Client
	xmux      *xmuxLease
	owned     bool
	closeOnce sync.Once
}

func (l *httpClientLease) consumeRequest() {
	if l.xmux != nil {
		l.xmux.consumeRequest()
	}
}

func (l *httpClientLease) Close() {
	l.closeOnce.Do(func() {
		if l.xmux != nil {
			l.xmux.release()
		}
		if l.owned {
			closeIdleConnections(l.client)
		}
	})
}

type xmuxClient struct {
	httpClient   *http.Client
	openUsage    int32
	leftUsage    int32
	leftRequests int32
	unreusableAt time.Time
	retiring     bool
}

type xmuxManager struct {
	access             sync.Mutex
	config             *option.V2RayXHTTPXmuxConfig
	concurrency        int32
	desiredConnections int
	maxConnections     int
	newFunc            func() *http.Client
	clients            []*xmuxClient
	notify             chan struct{}
	closed             bool
	useSafeDefaults    bool
}

func newXmuxManager(config *option.V2RayXHTTPXmuxConfig, newFunc func() *http.Client) *xmuxManager {
	manager := &xmuxManager{
		config:         config,
		maxConnections: defaultXmuxPoolLimit,
		newFunc:        newFunc,
		notify:         make(chan struct{}),
	}
	manager.useSafeDefaults = xmuxSettingsAreZero(config)
	if manager.useSafeDefaults {
		manager.desiredConnections = 3
		manager.maxConnections = 3
	} else {
		manager.concurrency = positiveRangeValue(config.MaxConcurrency)
	}
	if connections := positiveRangeValue(config.MaxConnections); connections > 0 {
		if connections > hardXmuxPoolLimit {
			connections = hardXmuxPoolLimit
		}
		manager.desiredConnections = int(connections)
		manager.maxConnections = int(connections)
	}
	return manager
}

func (m *xmuxManager) acquire(ctx context.Context) (*xmuxLease, error) {
	for {
		m.access.Lock()
		m.pruneLocked(time.Now())
		if m.closed {
			m.access.Unlock()
			return nil, net.ErrClosed
		}

		if m.desiredConnections > len(m.clients) {
			client := m.newClientLocked()
			m.acquireClientLocked(client)
			m.access.Unlock()
			return &xmuxLease{manager: m, client: client}, nil
		}
		if client := m.availableClientLocked(); client != nil {
			m.acquireClientLocked(client)
			m.access.Unlock()
			return &xmuxLease{manager: m, client: client}, nil
		}
		if len(m.clients) < m.maxConnections {
			client := m.newClientLocked()
			m.acquireClientLocked(client)
			m.access.Unlock()
			return &xmuxLease{manager: m, client: client}, nil
		}

		notify := m.notify
		m.access.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notify:
		}
	}
}

func (m *xmuxManager) newClientLocked() *xmuxClient {
	client := &xmuxClient{
		httpClient:   m.newFunc(),
		leftUsage:    -1,
		leftRequests: math.MaxInt32,
	}
	if m.useSafeDefaults {
		client.leftRequests = randRange(600, 900)
		client.unreusableAt = time.Now().Add(time.Duration(randRange(1800, 3000)) * time.Second)
	}
	if reuseTimes := positiveRangeValue(m.config.CMaxReuseTimes); reuseTimes > 0 {
		client.leftUsage = reuseTimes
	}
	if requestTimes := positiveRangeValue(m.config.HMaxRequestTimes); requestTimes > 0 {
		client.leftRequests = requestTimes
	}
	if reusableSeconds := positiveRangeValue(m.config.HMaxReusableSecs); reusableSeconds > 0 {
		client.unreusableAt = time.Now().Add(time.Duration(reusableSeconds) * time.Second)
	}
	m.clients = append(m.clients, client)
	return client
}

func (m *xmuxManager) acquireClientLocked(client *xmuxClient) {
	client.openUsage++
	if client.leftUsage > 0 {
		client.leftUsage--
		if client.leftUsage == 0 {
			client.retiring = true
		}
	}
}

func (m *xmuxManager) availableClientLocked() *xmuxClient {
	var selected *xmuxClient
	for _, client := range m.clients {
		if client.retiring || client.leftRequests <= 0 {
			continue
		}
		if !client.unreusableAt.IsZero() && time.Now().After(client.unreusableAt) {
			client.retiring = true
			continue
		}
		if m.concurrency > 0 && client.openUsage >= m.concurrency {
			continue
		}
		if selected == nil || client.openUsage < selected.openUsage {
			selected = client
		}
	}
	return selected
}

func (m *xmuxManager) release(client *xmuxClient) {
	m.access.Lock()
	if client.openUsage > 0 {
		client.openUsage--
	}
	m.pruneLocked(time.Now())
	m.signalLocked()
	m.access.Unlock()
}

func (m *xmuxManager) consumeRequest(client *xmuxClient) {
	m.access.Lock()
	if client.leftRequests != math.MaxInt32 && client.leftRequests > 0 {
		client.leftRequests--
		if client.leftRequests == 0 {
			client.retiring = true
		}
	}
	m.signalLocked()
	m.access.Unlock()
}

func (m *xmuxManager) pruneLocked(now time.Time) {
	for index := 0; index < len(m.clients); {
		client := m.clients[index]
		if client.leftRequests <= 0 || (!client.unreusableAt.IsZero() && now.After(client.unreusableAt)) {
			client.retiring = true
		}
		if client.retiring && client.openUsage == 0 {
			closeIdleConnections(client.httpClient)
			m.clients = append(m.clients[:index], m.clients[index+1:]...)
			continue
		}
		index++
	}
}

func (m *xmuxManager) closeAll() {
	m.access.Lock()
	if m.closed {
		m.access.Unlock()
		return
	}
	m.closed = true
	clients := m.clients
	m.clients = nil
	m.signalLocked()
	m.access.Unlock()
	for _, client := range clients {
		closeIdleConnections(client.httpClient)
	}
}

func (m *xmuxManager) signalLocked() {
	close(m.notify)
	m.notify = make(chan struct{})
}

type xmuxLease struct {
	manager     *xmuxManager
	client      *xmuxClient
	releaseOnce sync.Once
}

func (l *xmuxLease) consumeRequest() {
	l.manager.consumeRequest(l.client)
}

func (l *xmuxLease) release() {
	l.releaseOnce.Do(func() { l.manager.release(l.client) })
}

func closeIdleConnections(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	if closer, loaded := client.Transport.(interface{ CloseIdleConnections() }); loaded {
		closer.CloseIdleConnections()
	}
}

func positiveRangeValue(config *option.V2RayXHTTPRangeConfig) int32 {
	if config == nil {
		return 0
	}
	from := config.From
	to := config.To
	if from < 0 {
		from = 0
	}
	if to < from {
		to = from
	}
	return randRange(from, to)
}

func xmuxSettingsAreZero(config *option.V2RayXHTTPXmuxConfig) bool {
	return !rangeHasPositiveValue(config.MaxConcurrency) &&
		!rangeHasPositiveValue(config.MaxConnections) &&
		!rangeHasPositiveValue(config.CMaxReuseTimes) &&
		!rangeHasPositiveValue(config.HMaxRequestTimes) &&
		!rangeHasPositiveValue(config.HMaxReusableSecs) &&
		config.HKeepAlivePeriod == 0
}

func rangeHasPositiveValue(config *option.V2RayXHTTPRangeConfig) bool {
	return config != nil && (config.From > 0 || config.To > 0)
}
