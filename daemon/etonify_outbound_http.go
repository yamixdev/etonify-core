package daemon

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/ntp"
)

const (
	outboundHTTPDefaultMaxBytes = 1024 * 1024
	outboundHTTPMaximumBytes    = 3 * 1024 * 1024
	outboundHTTPDefaultTimeout  = 10 * time.Second
	outboundHTTPMaximumTimeout  = 60 * time.Second
	outboundHTTPMaxRedirects    = 5
)

func (s *StartedService) FetchURLViaOutbound(ctx context.Context, request *OutboundHTTPFetchRequest) (*OutboundHTTPFetchResponse, error) {
	if request == nil || strings.TrimSpace(request.OutboundTag) == "" {
		return nil, os.ErrInvalid
	}
	requestURL, err := validateOutboundHTTPURL(request.Url)
	if err != nil {
		return nil, err
	}
	maximumBytes := int(request.MaxBytes)
	if maximumBytes <= 0 {
		maximumBytes = outboundHTTPDefaultMaxBytes
	}
	if maximumBytes > outboundHTTPMaximumBytes {
		return nil, fmt.Errorf("outbound HTTP response limit exceeds %d bytes", outboundHTTPMaximumBytes)
	}
	timeout := time.Duration(request.TimeoutMillis) * time.Millisecond
	if timeout <= 0 {
		timeout = outboundHTTPDefaultTimeout
	}
	if timeout > outboundHTTPMaximumTimeout {
		return nil, fmt.Errorf("outbound HTTP timeout exceeds %s", outboundHTTPMaximumTimeout)
	}

	s.serviceAccess.RLock()
	if s.serviceStatus.Status != ServiceStatus_STARTED || s.instance == nil {
		s.serviceAccess.RUnlock()
		return nil, os.ErrInvalid
	}
	boxService := s.instance
	outbound, err := resolveSelectedURLTestOutbound(boxService.outboundManager, strings.TrimSpace(request.OutboundTag))
	s.serviceAccess.RUnlock()
	if err != nil {
		return nil, err
	}

	fetchContext, cancel := context.WithTimeout(ctx, timeout)
	stopInstanceCancellation := context.AfterFunc(boxService.ctx, cancel)
	defer stopInstanceCancellation()
	defer cancel()
	return fetchURLViaOutbound(fetchContext, boxService.ctx, outbound, requestURL, request.Headers, maximumBytes)
}

func fetchURLViaOutbound(
	ctx context.Context,
	instanceContext context.Context,
	outbound adapter.Outbound,
	requestURL *url.URL,
	headers map[string]string,
	maximumBytes int,
) (*OutboundHTTPFetchResponse, error) {
	resolveDialer := dialer.NewResolveDialer(instanceContext, outbound, true, "", adapter.DNSQueryOptions{}, 0)
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			return resolveDialer.DialContext(ctx, network, M.ParseSocksaddr(address))
		},
		TLSClientConfig: &tls.Config{
			Time:    ntp.TimeFuncFromContext(instanceContext),
			RootCAs: adapter.RootPoolFromContext(instanceContext),
		},
		// A custom DialContext disables Go's automatic HTTP/2 attempt unless it
		// is requested explicitly. Subscription providers commonly prefer h2,
		// so keep it enabled even though this one-shot transport does not retain
		// idle connections after the RPC completes.
		ForceAttemptHTTP2: true,
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(next *http.Request, previous []*http.Request) error {
			if len(previous) >= outboundHTTPMaxRedirects {
				return http.ErrUseLastResponse
			}
			if previous[len(previous)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
				return fmt.Errorf("HTTPS to HTTP redirect is not allowed")
			}
			_, err := validateOutboundHTTPURL(next.URL.String())
			return err
		},
	}
	defer client.CloseIdleConnections()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	for name, value := range headers {
		if outboundHTTPHeaderAllowed(name) {
			httpRequest.Header.Set(name, value)
		}
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > int64(maximumBytes) {
		return nil, fmt.Errorf("outbound HTTP response exceeds %d bytes", maximumBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maximumBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maximumBytes {
		return nil, fmt.Errorf("outbound HTTP response exceeds %d bytes", maximumBytes)
	}
	responseHeaders := make(map[string]string)
	for name, values := range response.Header {
		responseHeaders[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	return &OutboundHTTPFetchResponse{
		StatusCode: int32(response.StatusCode),
		Body:       body,
		Headers:    responseHeaders,
		FinalUrl:   response.Request.URL.String(),
	}, nil
}

func validateOutboundHTTPURL(rawURL string) (*url.URL, error) {
	requestURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || requestURL.Hostname() == "" || requestURL.User != nil {
		return nil, os.ErrInvalid
	}
	switch strings.ToLower(requestURL.Scheme) {
	case "https":
		return requestURL, nil
	case "http":
		host := strings.ToLower(requestURL.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return requestURL, nil
		}
	}
	return nil, fmt.Errorf("outbound HTTP fetch requires HTTPS outside the local device")
}

func outboundHTTPHeaderAllowed(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "connection", "content-length", "host", "proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return false
	default:
		return true
	}
}
