package group

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	U "github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/stretchr/testify/require"
)

type urlTestSelectionOutbound struct {
	tag          string
	dialAttempts *atomic.Int32
}

func (o *urlTestSelectionOutbound) Type() string           { return "test" }
func (o *urlTestSelectionOutbound) Tag() string            { return o.tag }
func (o *urlTestSelectionOutbound) Network() []string      { return []string{N.NetworkTCP, N.NetworkUDP} }
func (o *urlTestSelectionOutbound) Dependencies() []string { return nil }
func (o *urlTestSelectionOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	if o.dialAttempts != nil {
		o.dialAttempts.Add(1)
	}
	return nil, net.ErrClosed
}
func (o *urlTestSelectionOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func TestURLTestSelectionIgnoresUnavailableHistory(t *testing.T) {
	t.Parallel()

	unavailable := &urlTestSelectionOutbound{tag: "unavailable"}
	available := &urlTestSelectionOutbound{tag: "available"}
	history := U.NewHistoryStorage()
	history.StoreURLTestHistory(unavailable.Tag(), &adapter.URLTestHistory{
		Time:   time.Now(),
		Status: adapter.URLTestStatusUnavailable,
		Error:  "request timed out",
	})
	history.StoreURLTestHistory(available.Tag(), &adapter.URLTestHistory{
		Time:   time.Now(),
		Delay:  80,
		Status: adapter.URLTestStatusAvailable,
	})
	group := &URLTestGroup{
		outbounds:           []adapter.Outbound{available, unavailable},
		history:             history,
		tolerance:           50,
		interruptGroup:      interrupt.NewGroup(),
		selectedOutboundTCP: unavailable,
		selectedOutboundUDP: unavailable,
	}

	selected, availableHistory := group.Select(N.NetworkTCP)
	require.True(t, availableHistory)
	require.Same(t, available, selected)

	group.performUpdateCheck()
	selectedTCP, selectedUDP := group.selectedOutbounds()
	require.Same(t, available, selectedTCP)
	require.Same(t, available, selectedUDP)
}

func TestURLTestSelectionKeepsHealthyOutboundWithinTolerance(t *testing.T) {
	t.Parallel()

	selected := &urlTestSelectionOutbound{tag: "selected"}
	slightlyFaster := &urlTestSelectionOutbound{tag: "slightly-faster"}
	history := U.NewHistoryStorage()
	history.StoreURLTestHistory(selected.Tag(), &adapter.URLTestHistory{
		Time:   time.Now(),
		Delay:  100,
		Status: adapter.URLTestStatusAvailable,
	})
	history.StoreURLTestHistory(slightlyFaster.Tag(), &adapter.URLTestHistory{
		Time:   time.Now(),
		Delay:  70,
		Status: adapter.URLTestStatusAvailable,
	})
	group := &URLTestGroup{
		outbounds:           []adapter.Outbound{slightlyFaster, selected},
		history:             history,
		tolerance:           50,
		interruptGroup:      interrupt.NewGroup(),
		selectedOutboundTCP: selected,
	}

	outbound, hasHistory := group.Select(N.NetworkTCP)
	require.True(t, hasHistory)
	require.Same(t, selected, outbound)
}

func TestURLTestSelectionUsesStaleFallbackUntilFreshNetworkResult(t *testing.T) {
	t.Parallel()

	staleFast := &urlTestSelectionOutbound{tag: "stale-fast"}
	freshSlower := &urlTestSelectionOutbound{tag: "fresh-slower"}
	history := U.NewHistoryStorage()
	history.StoreURLTestHistory(staleFast.Tag(), &adapter.URLTestHistory{
		Time:  time.Now(),
		Delay: 25,
	})
	history.StoreURLTestHistory(freshSlower.Tag(), &adapter.URLTestHistory{
		Time:  time.Now(),
		Delay: 40,
	})
	history.ResetNetwork()
	group := &URLTestGroup{
		outbounds:           []adapter.Outbound{staleFast, freshSlower},
		history:             history,
		tolerance:           50,
		interruptGroup:      interrupt.NewGroup(),
		selectedOutboundTCP: staleFast,
	}

	selected, availableHistory := group.Select(N.NetworkTCP)
	require.True(t, availableHistory)
	require.Same(t, staleFast, selected)

	require.True(t, history.StoreForGeneration(
		history.Generation(),
		freshSlower.Tag(),
		&adapter.URLTestHistory{Time: time.Now(), Delay: 90},
	))
	selected, availableHistory = group.Select(N.NetworkTCP)
	require.True(t, availableHistory)
	require.Same(t, freshSlower, selected)

	group.performUpdateCheck()
	selectedTCP, selectedUDP := group.selectedOutbounds()
	require.Same(t, freshSlower, selectedTCP)
	require.Same(t, freshSlower, selectedUDP)
}

func TestExternallyManagedURLTestGroupDoesNotStartOwnScheduler(t *testing.T) {
	history := U.NewHistoryStorage()
	history.SetExternallyManaged(true)
	group := &URLTestGroup{
		ctx:            context.Background(),
		history:        history,
		interval:       time.Second,
		close:          make(chan struct{}),
		interruptGroup: interrupt.NewGroup(),
	}

	group.PostStart()
	group.Touch()

	require.True(t, group.started)
	require.Nil(t, group.ticker)
}

func TestExternallyManagedURLTestDoesNotProbeAllOutboundsOnInterfaceChange(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())
	history := U.NewHistoryStorage()
	history.SetExternallyManaged(true)
	var dialAttempts atomic.Int32
	probe := &urlTestSelectionOutbound{tag: "probe", dialAttempts: &dialAttempts}
	group := &URLTestGroup{
		ctx:            ctx,
		outbounds:      []adapter.Outbound{probe},
		history:        history,
		pause:          service.FromContext[pause.Manager](ctx),
		logger:         log.NewNOPFactory().Logger(),
		interruptGroup: interrupt.NewGroup(),
	}
	(&URLTest{group: group}).InterfaceUpdated(ctx)
	require.Never(t, func() bool { return dialAttempts.Load() > 0 }, 200*time.Millisecond, 10*time.Millisecond)
}

func TestUnmanagedURLTestStillProbesOnInterfaceChange(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())
	history := U.NewHistoryStorage()
	var dialAttempts atomic.Int32
	probe := &urlTestSelectionOutbound{tag: "probe", dialAttempts: &dialAttempts}
	group := &URLTestGroup{
		ctx:            ctx,
		outbounds:      []adapter.Outbound{probe},
		history:        history,
		pause:          service.FromContext[pause.Manager](ctx),
		logger:         log.NewNOPFactory().Logger(),
		interruptGroup: interrupt.NewGroup(),
	}
	(&URLTest{group: group}).InterfaceUpdated(ctx)
	require.Eventually(t, func() bool { return dialAttempts.Load() > 0 }, time.Second, 10*time.Millisecond)
}

func TestURLTestScopeClosesGroupBeforeStartedStage(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())
	history := U.NewHistoryStorage()
	history.SetExternallyManaged(true)
	ctx = service.ContextWithPtr(ctx, history)
	probe := &urlTestSelectionOutbound{tag: "probe"}
	manager := &selectorInterruptManager{items: map[string]adapter.Outbound{"probe": probe}}
	scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	outbound := &URLTest{ctx: ctx, outbound: manager, tags: []string{"probe"}, logger: log.NewNOPFactory().Logger()}
	require.NoError(t, outbound.Start(adapter.StartStateStart, scope))
	require.NoError(t, scope.Close())
	require.ErrorIs(t, outbound.group.ctx.Err(), context.Canceled)

	// A late finish-start or connection must not resurrect a closed scheduler.
	outbound.group.PostStart()
	outbound.group.Touch()
	require.False(t, outbound.group.started)
	require.Nil(t, outbound.group.ticker)
	select {
	case <-outbound.group.close:
	default:
		t.Fatal("scope close did not tear down group before finish-start")
	}
}

type cancelAwareURLTestOutbound struct {
	urlTestSelectionOutbound
	started chan struct{}
	once    sync.Once
}

func (o *cancelAwareURLTestOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	o.once.Do(func() { close(o.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestURLTestGroupCloseCancelsManualProbeWithoutTicker(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())
	history := U.NewHistoryStorage()
	ctx = service.ContextWithPtr(ctx, history)
	probe := &cancelAwareURLTestOutbound{urlTestSelectionOutbound: urlTestSelectionOutbound{tag: "probe"}, started: make(chan struct{})}
	group, err := NewURLTestGroup(ctx, nil, log.NewNOPFactory().Logger(), []adapter.Outbound{probe}, "http://example.com", time.Second, 50, time.Minute, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = group.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = group.URLTest(context.Background())
	}()
	select {
	case <-probe.started:
	case <-time.After(time.Second):
		t.Fatal("manual probe did not start")
	}
	require.NoError(t, group.Close())
	require.NoError(t, group.Close())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("group close did not cancel the manual probe")
	}
	require.Nil(t, history.LoadCurrentURLTestHistory("probe"), "shutdown cancellation must not invalidate probe history")
}
