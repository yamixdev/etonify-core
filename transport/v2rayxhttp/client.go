package v2rayxhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
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
	defaultH2ReadIdle    = 30 * time.Second
	minimumH2ReadIdle    = 5 * time.Second
	maximumH2ReadIdle    = 5 * time.Minute
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
	closeOnce        sync.Once
	closeDone        chan struct{}
	closeErr         error

	xmuxAccess  sync.Mutex
	xmuxManager *xmuxManager
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayXHTTPOptions, tlsConfig tls.Config) (*Client, error) {
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
			IdleConnTimeout:  90 * time.Second,
			ReadIdleTimeout:  c.http2ReadIdleTimeout(),
			PingTimeout:      15 * time.Second,
			WriteByteTimeout: 30 * time.Second,
		}
	} else {
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return c.dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
			},
			IdleConnTimeout:   90 * time.Second,
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
	period := time.Duration(c.options.Xmux.HKeepAlivePeriod)
	period = min(max(period, minimumH2ReadIdle/time.Second), maximumH2ReadIdle/time.Second)
	return period * time.Second
}

func (c *Client) getHTTPClient(ctx context.Context) (*httpClientLease, error) {
	if c.options.Xmux == nil {
		return &httpClientLease{client: c.createHTTPClient(), owned: true}, nil
	}

	c.xmuxAccess.Lock()
	if c.xmuxManager == nil {
		c.xmuxManager = newXmuxManager(c.options.Xmux, c.createHTTPClient)
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
	if c.lifecycleContext.Err() != nil {
		return nil, net.ErrClosed
	}
	generation, available := c.beginDial()
	if !available {
		return nil, net.ErrClosed
	}
	requestContext, cancelRequests := context.WithCancel(ctx)
	stopLifecycleCancel := context.AfterFunc(c.lifecycleContext, cancelRequests)

	httpLease, err := c.getHTTPClient(requestContext)
	if err != nil {
		stopLifecycleCancel()
		cancelRequests()
		return nil, err
	}

	mode := c.config.mode
	if mode == "" || mode == "auto" {
		mode = resolveMode(mode, c.http2, c.reality)
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
		err = c.dialStreamOne(requestContext, httpLease, downloadReader, uploadReader)
	case "stream-up":
		err = c.dialStreamUp(requestContext, httpLease, downloadReader, sessionID, uploadReader)
	default:
		err = c.dialPacketUp(requestContext, httpLease, downloadReader, sessionID, uploadReader)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
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

func (c *Client) dialStreamOne(ctx context.Context, lease *httpClientLease, download *waitReadCloser, upload *io.PipeReader) error {
	request, err := c.buildRequest(ctx, c.config.uplinkHTTPMethod, "", "", upload)
	if err != nil {
		return err
	}
	lease.consumeRequest()
	go func() {
		response, doErr := lease.client.Do(request)
		if doErr != nil {
			_ = upload.CloseWithError(doErr)
			download.Fail(doErr)
			return
		}
		if response.StatusCode != http.StatusOK {
			statusErr := unexpectedStatus("stream-one", response)
			_ = upload.CloseWithError(statusErr)
			download.Fail(statusErr)
			return
		}
		download.Set(response.Body)
	}()
	return nil
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
			_ = upload.CloseWithError(doErr)
			download.Fail(doErr)
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
		_ = upload.CloseWithError(err)
		download.Fail(err)
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
		return err
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
	sessions := make([]*splitConn, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.xmuxAccess.Lock()
	manager := c.xmuxManager
	c.xmuxManager = nil
	c.xmuxAccess.Unlock()
	c.stateAccess.Unlock()

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
	sessions := make([]*splitConn, 0, len(c.sessions))
	for session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.stateAccess.Unlock()

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

func resolveMode(mode string, http2Enabled bool, realityEnabled bool) string {
	if mode != "" && mode != "auto" {
		return mode
	}
	if realityEnabled {
		return "stream-one"
	}
	if http2Enabled {
		return "stream-up"
	}
	return "packet-up"
}

func unexpectedStatus(operation string, response *http.Response) error {
	status := response.Status
	_ = response.Body.Close()
	return E.New("xhttp: unexpected ", operation, " status: ", status)
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
		manager.concurrency = randRange(16, 32)
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
