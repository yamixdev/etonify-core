package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/probe"
	"github.com/sagernet/sing-box/common/urltest"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

func TestNormalizeURLTestOptions(t *testing.T) {
	options := normalizeURLTestOptions(&URLTestRequest{
		UrlTestUrl:     " https://example.com/ping ",
		TimeoutMillis:  1,
		Concurrency:    100,
		DeadlineMillis: 100,
	})
	require.Equal(t, "https://example.com/ping", options.link)
	require.Equal(t, minimumURLTestTimeout, options.timeout)
	require.Equal(t, minimumURLTestTimeout, options.deadline)
	require.Equal(t, maximumURLTestConcurrency, options.concurrency)
}

func TestNormalizeURLTestSessionModes(t *testing.T) {
	manual := normalizeURLTestOptions(&URLTestRequest{Mode: urlTestModeManual})
	require.Equal(t, urlTestModeManual, manual.mode)
	require.Zero(t, manual.deadline)
	require.Equal(t, defaultURLTestConcurrency, manual.concurrency)

	background := normalizeURLTestOptions(&URLTestRequest{})
	require.Equal(t, urlTestModeBackground, background.mode)
	require.Equal(t, defaultURLTestDeadline, background.deadline)
	require.Equal(t, backgroundURLTestConcurrency, background.concurrency)

	targeted := normalizeURLTestOptions(&URLTestRequest{
		Mode:              urlTestModeManual,
		TargetOutboundTag: "proxy-1",
		Concurrency:       12,
	})
	require.Equal(t, urlTestModeTargeted, targeted.mode)
	require.Equal(t, 1, targeted.concurrency)
	require.Equal(t, defaultURLTestDeadline, targeted.deadline)
}

func TestValidateURLTestLink(t *testing.T) {
	require.NoError(t, validateURLTestLink(""))
	require.NoError(t, validateURLTestLink("https://example.com/generate_204"))
	require.Error(t, validateURLTestLink("file:///tmp/probe"))
	require.Error(t, validateURLTestLink("https:///missing-host"))
}

func TestRunURLTestTargetsBoundsConcurrency(t *testing.T) {
	targets := make([]urlTestTarget, 12)
	var active atomic.Int32
	var maximum atomic.Int32
	var completed atomic.Int32
	runURLTestTargets(context.Background(), targets, urlTestSessionOptions{
		timeout:     time.Second,
		concurrency: 3,
	}, func(context.Context, string, adapter.Outbound) (uint16, error) {
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return 20, nil
	}, func(urlTestTarget, uint16, error) {
		completed.Add(1)
	})
	require.Equal(t, int32(len(targets)), completed.Load())
	require.LessOrEqual(t, maximum.Load(), int32(3))
	require.Greater(t, maximum.Load(), int32(1))
}

func TestRunURLTestTargetsStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	targets := make([]urlTestTarget, 20)
	var calls atomic.Int32
	var results atomic.Int32
	var once sync.Once
	runURLTestTargets(ctx, targets, urlTestSessionOptions{
		timeout:     time.Second,
		concurrency: 1,
	}, func(context.Context, string, adapter.Outbound) (uint16, error) {
		calls.Add(1)
		once.Do(cancel)
		return 20, nil
	}, func(urlTestTarget, uint16, error) {
		results.Add(1)
	})
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, int32(1), results.Load())
}

func TestClassifyURLTestError(t *testing.T) {
	code, _ := classifyURLTestError(context.DeadlineExceeded)
	require.Equal(t, "timeout", code)
	code, _ = classifyURLTestError(errors.New("remote error: tls: bad certificate"))
	require.Equal(t, "tls", code)
	code, message := classifyURLTestError(classifiedURLTestError{})
	require.Equal(t, "xhttp_response_closed", code)
	require.Equal(t, "XHTTP response stream failed", message)
}

type classifiedURLTestError struct{}

func (classifiedURLTestError) Error() string               { return "raw private transport detail" }
func (classifiedURLTestError) URLTestErrorCode() string    { return "xhttp_response_closed" }
func (classifiedURLTestError) URLTestErrorMessage() string { return "XHTTP response stream failed" }

var _ probe.ClassifiedError = classifiedURLTestError{}

func TestPrioritizeURLTestTargetsMakesProgressAcrossLargeProfiles(t *testing.T) {
	history := urltest.NewHistoryStorage()
	now := time.Now()
	history.StoreURLTestHistory("fresh", &adapter.URLTestHistory{Time: now})
	history.StoreURLTestHistory("old", &adapter.URLTestHistory{Time: now.Add(-time.Hour)})
	targets := []urlTestTarget{
		{tag: "fresh"},
		{tag: "missing"},
		{tag: "old"},
	}

	prioritizeURLTestTargets(targets, history)

	require.Equal(t, []string{"missing", "old", "fresh"}, []string{
		targets[0].tag,
		targets[1].tag,
		targets[2].tag,
	})
}

func TestResolveURLTestTargetsForRequestOnlyIncludesPendingConcreteLeaves(t *testing.T) {
	manager := selectionTestManager{outbounds: map[string]adapter.Outbound{
		"a":        mockLeafOutbound{tag: "a"},
		"b":        mockLeafOutbound{tag: "b"},
		"c":        mockLeafOutbound{tag: "c"},
		"provider": selectionTestGroup{tag: "provider", children: []string{"a", "b"}},
		"select":   selectionTestGroup{tag: "select", children: []string{"provider", "c", "b"}},
	}}
	boxService := &Instance{outboundManager: manager, urlTestHistoryStorage: urltest.NewHistoryStorage()}
	request := &URLTestRequest{
		OutboundTag:          "select",
		IncludeOutboundTags:  []string{"b", "b", "a"},
		PriorityOutboundTag:  "b",
		LogicalSessionId:     "manual-7",
		PhysicalNetworkEpoch: 8,
		Mode:                 urlTestModeManual,
	}
	targets, err := resolveURLTestTargetsForRequest(boxService, request)
	require.NoError(t, err)
	require.Equal(t, []string{"b", "a"}, []string{targets[0].tag, targets[1].tag})

	request.IncludeOutboundTags = nil
	all, err := resolveURLTestTargetsForRequest(boxService, request)
	require.NoError(t, err)
	require.Len(t, all, 3)

	request.IncludeOutboundTags = []string{"missing"}
	_, err = resolveURLTestTargetsForRequest(boxService, request)
	require.ErrorContains(t, err, "missing")

	request.IncludeOutboundTags = []string{"provider"}
	_, err = resolveURLTestTargetsForRequest(boxService, request)
	require.ErrorContains(t, err, "provider")
}

func TestResolveURLTestTargetsForRequestLargeGroupDeduplicatesLeaves(t *testing.T) {
	const count = 5000
	manager := selectionTestManager{outbounds: make(map[string]adapter.Outbound, count+2)}
	tags := make([]string, 0, count+1)
	for index := range count {
		tag := fmt.Sprintf("leaf-%04d", index)
		manager.outbounds[tag] = mockLeafOutbound{tag: tag}
		tags = append(tags, tag)
	}
	tags = append(tags, "leaf-0000")
	manager.outbounds["select"] = selectionTestGroup{tag: "select", children: tags}
	boxService := &Instance{outboundManager: manager, urlTestHistoryStorage: urltest.NewHistoryStorage()}
	targets, err := resolveURLTestTargetsForRequest(boxService, &URLTestRequest{
		OutboundTag:         "select",
		IncludeOutboundTags: []string{"leaf-4999", "leaf-4999", "leaf-0000"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"leaf-0000", "leaf-4999"}, []string{targets[0].tag, targets[1].tag})
}

func TestURLTestSessionMessagesPreserveLogicalIdentity(t *testing.T) {
	session := &urlTestSession{
		id:                   2,
		logicalSessionID:     "manual-7",
		physicalNetworkEpoch: 8,
		networkGeneration:    3,
		groupTag:             "select",
		total:                2,
	}
	history := &adapter.URLTestHistory{Time: time.UnixMilli(1234), Delay: 51, Status: adapter.URLTestStatusAvailable}
	result := urlTestResultMessage(session, "b", history, 9)
	require.Equal(t, "manual-7", result.LogicalSessionId)
	require.Equal(t, uint64(8), result.PhysicalNetworkEpoch)
	require.Equal(t, uint64(3), result.NetworkGeneration)
	require.Equal(t, int32(51), result.Delay)

	status := urlTestSessionStatusMessage(session, "running", "")
	require.Equal(t, "manual-7", status.LogicalSessionId)
	require.Equal(t, uint64(8), status.PhysicalNetworkEpoch)
	require.Equal(t, uint64(3), status.NetworkGeneration)
}

type selectionTestManager struct {
	adapter.OutboundManager
	outbounds map[string]adapter.Outbound
}

func (m selectionTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, ok := m.outbounds[tag]
	return outbound, ok
}

type selectionTestGroup struct {
	adapter.Outbound
	tag      string
	children []string
	refresh  func()
}

func (g selectionTestGroup) Tag() string                       { return g.tag }
func (g selectionTestGroup) Now() string                       { return "" }
func (g selectionTestGroup) All() []string                     { return g.children }
func (g selectionTestGroup) Selected(string) adapter.Outbound  { return nil }
func (g selectionTestGroup) AttachConnection(io.Closer) func() { return func() {} }
func (g selectionTestGroup) RefreshURLTestSelection() {
	if g.refresh != nil {
		g.refresh()
	}
}

func TestRefreshURLTestSelectionsChildrenBeforeParents(t *testing.T) {
	var order []string
	childSelected := false
	manager := selectionTestManager{outbounds: map[string]adapter.Outbound{}}
	manager.outbounds["provider"] = selectionTestGroup{
		tag: "provider", children: []string{"missing"},
		refresh: func() { childSelected = true; order = append(order, "provider") },
	}
	manager.outbounds["lowest"] = selectionTestGroup{
		tag: "lowest", children: []string{"provider", "provider"},
		refresh: func() {
			require.True(t, childSelected, "parent must compare the new child selection")
			order = append(order, "lowest")
		},
	}
	manager.outbounds["select"] = selectionTestGroup{
		tag: "select", children: []string{"lowest", "provider", "select"},
		refresh: func() { order = append(order, "select") },
	}
	refreshURLTestGroupSelections(manager, "select")
	require.Equal(t, []string{"provider", "lowest", "select"}, order)
}

func TestURLTestSelectionUpdaterRefreshesFirstResultAndCoalescesRest(t *testing.T) {
	var refreshCount atomic.Int32
	updater := newURLTestSelectionUpdater(40*time.Millisecond, func() {
		refreshCount.Add(1)
	})
	updater.onResult()
	require.Equal(t, int32(1), refreshCount.Load(), "first completed probe must update routing immediately")
	for range 20 {
		updater.onResult()
	}
	require.Equal(t, int32(1), refreshCount.Load(), "rapid results must not each rescan the group")
	require.Eventually(t, func() bool { return refreshCount.Load() == 2 }, time.Second, 5*time.Millisecond)
	updater.finish()
	require.Equal(t, int32(3), refreshCount.Load(), "final selection must see the entire queue")
}

func TestURLTestSessionRefreshesSelectionBeforeQueueDrains(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	history := urltest.NewHistoryStorage()
	firstSelection := make(chan struct{}, 1)
	manager := selectionTestManager{outbounds: map[string]adapter.Outbound{
		"fast": mockLeafOutbound{tag: "fast"},
		"slow": mockLeafOutbound{tag: "slow"},
	}}
	manager.outbounds["select"] = selectionTestGroup{
		tag: "select", children: []string{"fast", "slow"},
		refresh: func() {
			if result := history.LoadCurrentURLTestHistory("fast"); result != nil && result.Delay == 25 {
				select {
				case firstSelection <- struct{}{}:
				default:
				}
			}
		},
	}
	instance := &Instance{ctx: ctx, outboundManager: manager, urlTestHistoryStorage: history}
	targets := []urlTestTarget{
		{tag: "fast", outbound: manager.outbounds["fast"]},
		{tag: "slow", outbound: manager.outbounds["slow"]},
	}
	session := &urlTestSession{
		instance: instance, cancel: cancel, queue: newURLTestQueue(targets),
		networkGeneration: history.Generation(), ctx: ctx, groupTag: "select", full: true, total: len(targets),
	}
	service := &StartedService{urlTestSessions: map[string]*urlTestSession{"select": session}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.runURLTestSessionWithProbe(ctx, "select", "select", session, targets, urlTestSessionOptions{timeout: time.Second, concurrency: 2}, func(probeContext context.Context, _ string, outbound adapter.Outbound) (uint16, error) {
			if outbound.Tag() == "fast" {
				return 25, nil
			}
			<-probeContext.Done()
			return 0, probeContext.Err()
		})
	}()
	select {
	case <-firstSelection:
		// The slow probe is still blocked, so the queue has not drained.
	case <-time.After(time.Second):
		t.Fatal("first usable result did not update selection before queue completion")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled URLTest session did not stop")
	}
}

type mockLeafOutbound struct {
	adapter.Outbound
	tag string
}

func (m mockLeafOutbound) Tag() string { return m.tag }

func (m mockLeafOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("mock dial error")
}

func (m mockLeafOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("mock packet error")
}

func TestStartURLTestForceDoesNotJoinFullSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leaf1 := mockLeafOutbound{tag: "leaf-1"}
	leaf2 := mockLeafOutbound{tag: "leaf-2"}
	manager := selectionTestManager{outbounds: map[string]adapter.Outbound{
		"leaf-1": leaf1,
		"leaf-2": leaf2,
		"select": selectionTestGroup{
			tag:      "select",
			children: []string{"leaf-1", "leaf-2"},
		},
	}}

	history := urltest.NewHistoryStorage()
	boxService := &Instance{
		ctx:                   ctx,
		outboundManager:       manager,
		urlTestHistoryStorage: history,
	}

	s := &StartedService{
		serviceStatus:   &ServiceStatus{Status: ServiceStatus_STARTED},
		instance:        boxService,
		urlTestSessions: make(map[string]*urlTestSession),
	}
	defer s.cancelURLTestSessions()

	// 1. Start full session
	_, err := s.startURLTest(&URLTestRequest{
		OutboundTag: "select",
		Mode:        urlTestModeManual,
	})
	require.NoError(t, err)

	s.urlTestSessionAccess.Lock()
	fullSession := s.urlTestSessions["select"]
	s.urlTestSessionAccess.Unlock()
	require.NotNil(t, fullSession)
	require.True(t, fullSession.full)

	// 2. Targeted request without Force should join existing full session
	_, err = s.startURLTest(&URLTestRequest{
		OutboundTag:       "select",
		TargetOutboundTag: "leaf-1",
		Force:             false,
	})
	require.NoError(t, err)

	s.urlTestSessionAccess.Lock()
	require.Nil(t, s.urlTestSessions["select\x00target\x00leaf-1"])
	s.urlTestSessionAccess.Unlock()

	// 3. Targeted request WITH Force=true must NOT join full session; creates distinct targeted session
	_, err = s.startURLTest(&URLTestRequest{
		OutboundTag:       "select",
		TargetOutboundTag: "leaf-1",
		Force:             true,
	})
	require.NoError(t, err)

	s.urlTestSessionAccess.Lock()
	targetSession := s.urlTestSessions["select\x00target\x00leaf-1"]
	s.urlTestSessionAccess.Unlock()
	require.NotNil(t, targetSession)
	require.False(t, targetSession.full)
	require.Equal(t, "leaf-1", targetSession.targetTag)
	require.True(t, fullSession.isSuppressed("leaf-1"))
	require.False(t, fullSession.queue.remove("leaf-1"))
}
