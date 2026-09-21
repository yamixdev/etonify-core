package urltest

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/probe"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-mux"
	"github.com/sagernet/sing-snell"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
	"github.com/sagernet/sing/common/observable"
)

type HistoryStorage struct {
	access               sync.RWMutex
	generation           uint64
	delayHistory         map[string]networkHistoryEntry
	updateHooks          []*observable.Subscriber[struct{}]
	selectionUpdateHooks []*observable.Subscriber[struct{}]
	externallyManaged    bool
}

type networkHistoryEntry struct {
	generation uint64
	history    adapter.URLTestHistory
}

func NewHistoryStorage() *HistoryStorage {
	return &HistoryStorage{
		delayHistory: make(map[string]networkHistoryEntry),
	}
}

func (s *HistoryStorage) AddUpdateHook(hook *observable.Subscriber[struct{}]) {
	s.access.Lock()
	defer s.access.Unlock()
	s.updateHooks = append(s.updateHooks, hook)
}

// AddSelectionUpdateHook subscribes only to changes that may alter a URLTest
// group's selected outbound. Unlike AddUpdateHook, it is not notified for
// every individual latency result. UI clients can therefore refresh group
// topology and selection without rebuilding a full snapshot for every probe.
func (s *HistoryStorage) AddSelectionUpdateHook(hook *observable.Subscriber[struct{}]) {
	s.access.Lock()
	defer s.access.Unlock()
	s.selectionUpdateHooks = append(s.selectionUpdateHooks, hook)
}

// SetExternallyManaged hands probe scheduling to a graphical client's
// session manager. URLTest groups continue to consume history for routing,
// but do not start their own overlapping startup or periodic probes.
func (s *HistoryStorage) SetExternallyManaged(externallyManaged bool) {
	s.access.Lock()
	s.externallyManaged = externallyManaged
	s.access.Unlock()
}

func (s *HistoryStorage) ExternallyManaged() bool {
	if s == nil {
		return false
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return s.externallyManaged
}

func (s *HistoryStorage) NotifyUpdated() {
	s.access.RLock()
	updateHooks := append([]*observable.Subscriber[struct{}](nil), s.updateHooks...)
	selectionUpdateHooks := append(
		[]*observable.Subscriber[struct{}](nil),
		s.selectionUpdateHooks...,
	)
	s.access.RUnlock()
	notifyUpdated(updateHooks)
	notifyUpdated(selectionUpdateHooks)
}

func (s *HistoryStorage) LoadURLTestHistory(tag string) *adapter.URLTestHistory {
	return s.LoadCurrentURLTestHistory(tag)
}

// LoadFallbackURLTestHistory may return a successful measurement from the
// previous network generation. It is only for temporary routing continuity;
// telemetry and freshness decisions must use LoadCurrentURLTestHistory.
func (s *HistoryStorage) LoadFallbackURLTestHistory(tag string) *adapter.URLTestHistory {
	return s.loadURLTestHistory(tag, nil)
}

// LoadCurrentURLTestHistory returns only measurements made on the current
// network generation. Callers that present telemetry must use this method so
// a Wi-Fi result is never reported as a cellular result after a handover.
func (s *HistoryStorage) LoadCurrentURLTestHistory(tag string) *adapter.URLTestHistory {
	if s == nil {
		return nil
	}
	s.access.RLock()
	defer s.access.RUnlock()
	entry, loaded := s.delayHistory[tag]
	if !loaded || entry.generation != s.generation {
		return nil
	}
	historyCopy := entry.history
	return &historyCopy
}

// LoadURLTestHistoryForGeneration reads one entry from a caller-owned
// generation snapshot. It lets selection code make one coherent decision even
// if another network callback arrives while the group is being inspected.
func (s *HistoryStorage) LoadURLTestHistoryForGeneration(tag string, generation uint64) *adapter.URLTestHistory {
	return s.loadURLTestHistory(tag, &generation)
}

func (s *HistoryStorage) loadURLTestHistory(tag string, generation *uint64) *adapter.URLTestHistory {
	if s == nil {
		return nil
	}
	s.access.RLock()
	defer s.access.RUnlock()
	entry, loaded := s.delayHistory[tag]
	if !loaded ||
		(generation != nil && (*generation != s.generation || entry.generation != *generation)) {
		return nil
	}
	historyCopy := entry.history
	return &historyCopy
}

func (s *HistoryStorage) DeleteURLTestHistory(tag string) {
	s.access.Lock()
	delete(s.delayHistory, tag)
	updateHooks := append([]*observable.Subscriber[struct{}](nil), s.updateHooks...)
	s.access.Unlock()
	notifyUpdated(updateHooks)
}

func (s *HistoryStorage) StoreURLTestHistory(tag string, history *adapter.URLTestHistory) {
	if history == nil {
		s.DeleteURLTestHistory(tag)
		return
	}
	historyCopy := *history
	s.access.Lock()
	s.delayHistory[tag] = networkHistoryEntry{
		generation: s.generation,
		history:    historyCopy,
	}
	updateHooks := append([]*observable.Subscriber[struct{}](nil), s.updateHooks...)
	s.access.Unlock()
	notifyUpdated(updateHooks)
}

// Generation scopes measurements to one network. A probe captures it before
// dialing; results from an earlier network cannot overwrite current history.
func (s *HistoryStorage) Generation() uint64 {
	if s == nil {
		return 0
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return s.generation
}

func (s *HistoryStorage) ResetNetwork() {
	if s == nil {
		return
	}
	s.access.Lock()
	s.generation++
	hooks := append([]*observable.Subscriber[struct{}](nil), s.updateHooks...)
	s.access.Unlock()
	notifyUpdated(hooks)
}

func (s *HistoryStorage) StoreForGeneration(generation uint64, tag string, history *adapter.URLTestHistory) bool {
	if s == nil || history == nil {
		return false
	}
	s.access.Lock()
	if generation != s.generation {
		s.access.Unlock()
		return false
	}
	historyCopy := *history
	s.delayHistory[tag] = networkHistoryEntry{
		generation: generation,
		history:    historyCopy,
	}
	hooks := append([]*observable.Subscriber[struct{}](nil), s.updateHooks...)
	s.access.Unlock()
	notifyUpdated(hooks)
	return true
}

func notifyUpdated(updateHooks []*observable.Subscriber[struct{}]) {
	for _, updateHook := range updateHooks {
		updateHook.Emit(struct{}{})
	}
}

func (s *HistoryStorage) Close() error {
	s.access.Lock()
	defer s.access.Unlock()
	s.updateHooks = nil
	s.selectionUpdateHooks = nil
	return nil
}

func URLTest(ctx context.Context, link string, detour N.Dialer) (uint16, error) {
	probeSession := probe.NewURLTestSession()
	ctx = probe.WithURLTest(ctx, probeSession)
	for {
		delay, err := urlTestAttempt(ctx, link, detour)
		if err == nil {
			probeSession.Success()
			return delay, nil
		}
		if probeSession.Next(err) {
			continue
		}
		probeSession.Failure(err)
		return 0, err
	}
}

func urlTestAttempt(ctx context.Context, link string, detour N.Dialer) (uint16, error) {
	multiplexOutbound, isMultiplexOutbound := common.Cast[adapter.OutboundWithMultiplex](detour)
	if isMultiplexOutbound && multiplexOutbound.MultiplexEnabled() {
		warmContext := adapter.ContextWithKeepSession(ctx)
		warmContext = mux.ContextWithKeepSession(warmContext)
		warmContext = anytls.ContextWithKeepSession(warmContext)
		warmContext = contextWithQUICKeepSession(warmContext)
		warmContext = snell.ContextWithKeepSession(warmContext)
		_, err := urlTest(warmContext, link, detour)
		if err != nil {
			return 0, err
		}
	}
	return urlTest(ctx, link, detour)
}

func urlTest(ctx context.Context, link string, detour N.Dialer) (t uint16, err error) {
	if link == "" {
		link = "https://www.gstatic.com/generate_204"
	}
	linkURL, err := url.Parse(link)
	if err != nil {
		return
	}
	hostname := linkURL.Hostname()
	port := linkURL.Port()
	if port == "" {
		switch linkURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	start := time.Now()
	instance, err := detour.DialContext(ctx, "tcp", M.ParseSocksaddrHostPortStr(hostname, port))
	if err != nil {
		return
	}
	defer instance.Close()
	if N.NeedHandshakeForWrite(instance) {
		start = time.Now()
	}
	req, err := http.NewRequest(http.MethodHead, link, nil)
	if err != nil {
		return
	}
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return instance, nil
			},
			TLSClientConfig: &tls.Config{
				Time:    ntp.TimeFuncFromContext(ctx),
				RootCAs: adapter.RootPoolFromContext(ctx),
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: C.TCPTimeout,
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return
	}
	resp.Body.Close()
	t = uint16(time.Since(start) / time.Millisecond)
	return
}
