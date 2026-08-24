package daemon

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/ntp"
	"golang.org/x/sync/singleflight"
)

const (
	externalInfoPrimaryEndpoint  = "https://cloudflare.com/cdn-cgi/trace"
	externalInfoFallbackEndpoint = "https://api64.ipify.org"
	externalInfoAttemptTimeout   = 2 * time.Second
	externalInfoTimeout          = 4500 * time.Millisecond
	externalInfoCacheTTL         = 30 * time.Second
	externalInfoStaleTTL         = 2 * time.Minute
	externalInfoMaxBytes         = 64 * 1024
)

type outboundExternalInfo struct {
	ip          string
	countryCode string
}

type outboundExternalInfoCacheEntry struct {
	info      outboundExternalInfo
	refreshAt time.Time
	discardAt time.Time
}

type outboundExternalInfoResolver struct {
	access   sync.Mutex
	cache    map[string]outboundExternalInfoCacheEntry
	requests singleflight.Group
	fetch    outboundExternalInfoFetcher
}

type outboundExternalInfoFetcher func(context.Context, context.Context, adapter.Outbound) (outboundExternalInfo, error)

type outboundExternalInfoSource struct {
	name     string
	endpoint string
	parse    func([]byte) (outboundExternalInfo, error)
}

var outboundExternalInfoSources = []outboundExternalInfoSource{
	{name: "cloudflare", endpoint: externalInfoPrimaryEndpoint, parse: parseOutboundExternalInfo},
	{name: "ipify", endpoint: externalInfoFallbackEndpoint, parse: parsePlainExternalIP},
}

func newOutboundExternalInfoResolver() *outboundExternalInfoResolver {
	return &outboundExternalInfoResolver{
		cache: make(map[string]outboundExternalInfoCacheEntry),
		fetch: fetchOutboundExternalInfo,
	}
}

func (s *StartedService) LookupOutboundExternalInfo(ctx context.Context, request *OutboundExternalInfoRequest) (*OutboundExternalInfoResponse, error) {
	if request == nil || strings.TrimSpace(request.OutboundTag) == "" {
		return nil, os.ErrInvalid
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
	lookupContext, cancel := context.WithTimeout(ctx, externalInfoTimeout)
	stopInstanceCancellation := context.AfterFunc(boxService.ctx, cancel)
	defer stopInstanceCancellation()
	defer cancel()
	info, err := boxService.externalInfoResolver.lookup(lookupContext, boxService.ctx, outbound)
	if err != nil {
		return nil, err
	}
	return &OutboundExternalInfoResponse{Ip: info.ip, CountryCode: info.countryCode}, nil
}

func (r *outboundExternalInfoResolver) lookup(ctx context.Context, instanceContext context.Context, outbound adapter.Outbound) (outboundExternalInfo, error) {
	cacheKey := outbound.Tag()
	if cached, loaded := r.load(cacheKey, time.Now(), false); loaded {
		return cached, nil
	}
	resultChannel := r.requests.DoChan(cacheKey, func() (any, error) {
		if cached, loaded := r.load(cacheKey, time.Now(), false); loaded {
			return cached, nil
		}
		requestContext, cancel := context.WithTimeout(instanceContext, externalInfoTimeout)
		defer cancel()
		info, lookupErr := r.fetch(requestContext, instanceContext, outbound)
		if lookupErr != nil {
			if instanceErr := instanceContext.Err(); instanceErr != nil {
				return outboundExternalInfo{}, instanceErr
			}
			if stale, loaded := r.load(cacheKey, time.Now(), true); loaded {
				return stale, nil
			}
			return outboundExternalInfo{}, lookupErr
		}
		if stale, loaded := r.load(cacheKey, time.Now(), true); loaded && info.countryCode == "" && stale.ip == info.ip {
			info.countryCode = stale.countryCode
		}
		r.store(cacheKey, info, time.Now())
		return info, nil
	})
	select {
	case <-ctx.Done():
		return outboundExternalInfo{}, ctx.Err()
	case result := <-resultChannel:
		if result.Err != nil {
			return outboundExternalInfo{}, result.Err
		}
		return result.Val.(outboundExternalInfo), nil
	}
}

func (r *outboundExternalInfoResolver) load(key string, now time.Time, acceptStale bool) (outboundExternalInfo, bool) {
	r.access.Lock()
	defer r.access.Unlock()
	entry, loaded := r.cache[key]
	if !loaded {
		return outboundExternalInfo{}, false
	}
	if !now.Before(entry.discardAt) {
		delete(r.cache, key)
		return outboundExternalInfo{}, false
	}
	if !acceptStale && !now.Before(entry.refreshAt) {
		return outboundExternalInfo{}, false
	}
	return entry.info, true
}

func (r *outboundExternalInfoResolver) store(key string, info outboundExternalInfo, now time.Time) {
	r.access.Lock()
	r.cache[key] = outboundExternalInfoCacheEntry{
		info:      info,
		refreshAt: now.Add(externalInfoCacheTTL),
		discardAt: now.Add(externalInfoStaleTTL),
	}
	r.access.Unlock()
}

func fetchOutboundExternalInfo(ctx context.Context, instanceContext context.Context, outbound adapter.Outbound) (outboundExternalInfo, error) {
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
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()
	return fetchOutboundExternalInfoFromSources(ctx, client, outboundExternalInfoSources)
}

func fetchOutboundExternalInfoFromSources(ctx context.Context, client *http.Client, sources []outboundExternalInfoSource) (outboundExternalInfo, error) {
	if len(sources) == 0 {
		return outboundExternalInfo{}, fmt.Errorf("external info lookup has no configured sources")
	}
	var lookupErrors []error
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return outboundExternalInfo{}, err
		}
		attemptContext, cancel := context.WithTimeout(ctx, externalInfoAttemptTimeout)
		info, err := fetchOutboundExternalInfoSource(attemptContext, client, source)
		cancel()
		if err == nil {
			return info, nil
		}
		lookupErrors = append(lookupErrors, fmt.Errorf("%s: %w", source.name, err))
	}
	if err := ctx.Err(); err != nil {
		return outboundExternalInfo{}, err
	}
	return outboundExternalInfo{}, fmt.Errorf("external info lookup failed: %w", errors.Join(lookupErrors...))
}

func fetchOutboundExternalInfoSource(ctx context.Context, client *http.Client, source outboundExternalInfoSource) (outboundExternalInfo, error) {
	if source.endpoint == "" || source.parse == nil {
		return outboundExternalInfo{}, os.ErrInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.endpoint, nil)
	if err != nil {
		return outboundExternalInfo{}, err
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "Etonify-Core")
	response, err := client.Do(request)
	if err != nil {
		return outboundExternalInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return outboundExternalInfo{}, fmt.Errorf("external info service returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, externalInfoMaxBytes+1))
	if err != nil {
		return outboundExternalInfo{}, err
	}
	if len(body) > externalInfoMaxBytes {
		return outboundExternalInfo{}, fmt.Errorf("external info response is too large")
	}
	return source.parse(body)
}

func parseOutboundExternalInfo(content []byte) (outboundExternalInfo, error) {
	var info outboundExternalInfo
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "ip":
			address, err := netip.ParseAddr(strings.TrimSpace(value))
			if err == nil {
				info.ip = address.String()
			}
		case "loc":
			countryCode := strings.ToUpper(strings.TrimSpace(value))
			if isValidCountryCode(countryCode) && countryCode != "XX" {
				info.countryCode = countryCode
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return outboundExternalInfo{}, err
	}
	if info.ip == "" {
		return outboundExternalInfo{}, fmt.Errorf("external info response does not contain a valid IP address")
	}
	return info, nil
}

func parsePlainExternalIP(content []byte) (outboundExternalInfo, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(string(content)))
	if err != nil {
		return outboundExternalInfo{}, fmt.Errorf("external info response does not contain a valid IP address: %w", err)
	}
	return outboundExternalInfo{ip: address.String()}, nil
}

func isValidCountryCode(countryCode string) bool {
	return len(countryCode) == 2 &&
		countryCode[0] >= 'A' && countryCode[0] <= 'Z' &&
		countryCode[1] >= 'A' && countryCode[1] <= 'Z'
}
