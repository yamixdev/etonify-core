package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
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

func (g selectionTestGroup) Now() string              { return "" }
func (g selectionTestGroup) All() []string            { return g.children }
func (g selectionTestGroup) RefreshURLTestSelection() { g.refresh() }

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
