package route

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/winpowrprof"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/stretchr/testify/require"
)

type resetContextTestListener struct {
	adapter.Outbound
	contexts chan context.Context
	release  chan struct{}
	result   chan error
}

func (l *resetContextTestListener) InterfaceUpdated(ctx context.Context) {
	l.contexts <- ctx
	go func() {
		<-l.release
		l.result <- ctx.Err()
	}()
}

type resetContextTestOutboundManager struct {
	adapter.OutboundManager
	listener *resetContextTestListener
}

func (m *resetContextTestOutboundManager) Outbounds() []adapter.Outbound {
	return []adapter.Outbound{m.listener}
}

type resetContextTestEndpointManager struct{ adapter.EndpointManager }

func (*resetContextTestEndpointManager) Endpoints() []adapter.Endpoint { return nil }

type resetContextTestInboundManager struct{ adapter.InboundManager }

func (*resetContextTestInboundManager) Inbounds() []adapter.Inbound { return nil }

type resetContextTestRouter struct {
	adapter.Router
	reset chan struct{}
}

func (r *resetContextTestRouter) ResetNetwork() { r.reset <- struct{}{} }

type resetContextTestInterfaceMonitor struct {
	tun.DefaultInterfaceMonitor
	iif *control.Interface
}

func (m *resetContextTestInterfaceMonitor) DefaultInterface() *control.Interface { return m.iif }
func (*resetContextTestInterfaceMonitor) AndroidVPNEnabled() bool                { return false }

func TestNetworkResetContextRemainsAliveForAsyncListeners(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		notify func(*NetworkManager)
	}{
		{"interface", func(r *NetworkManager) { r.notifyInterfaceUpdate(nil, 0) }},
		{"power-resume", func(r *NetworkManager) { r.notifyWindowsPowerEvent(winpowrprof.EVENT_RESUME_AUTOMATIC) }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := pause.WithDefaultManager(context.Background())
			ctx = service.ContextWithPtr(ctx, urltest.NewHistoryStorage())
			scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
			t.Cleanup(func() { _ = scope.Close() })
			listener := &resetContextTestListener{
				contexts: make(chan context.Context, 2),
				release:  make(chan struct{}),
				result:   make(chan error, 2),
			}
			// Release asynchronous callbacks even if an assertion aborts the test.
			t.Cleanup(func() { close(listener.release) })
			router := &resetContextTestRouter{reset: make(chan struct{}, 2)}
			iif := control.Interface{Name: "test0", Index: 12345}
			manager := &NetworkManager{
				ctx:              ctx,
				startedCtx:       scope.Context(),
				logger:           log.NewNOPFactory().Logger(),
				pauseManager:     service.FromContext[pause.Manager](ctx),
				interfaceMonitor: &resetContextTestInterfaceMonitor{iif: &iif},
				endpoint:         &resetContextTestEndpointManager{},
				inbound:          &resetContextTestInboundManager{},
				outbound:         &resetContextTestOutboundManager{listener: listener},
				router:           router,
				wifiState:        adapter.WIFIState{SSID: "test-network"},
			}
			// Supply synthetic gateway data so environment updates never query
			// the host's routing or neighbor tables.
			manager.networkInterfaces.Store([]adapter.NetworkInterface{{
				Interface: iif,
				Gateways:  []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			}})
			waitNotification := func() context.Context {
				t.Helper()
				var notifiedCtx context.Context
				select {
				case notifiedCtx = <-listener.contexts:
				case <-time.After(time.Second):
					t.Fatal("reset listener was not notified")
				}
				select {
				case <-router.reset:
				case <-time.After(time.Second):
					t.Fatal("reset did not reach router")
				}
				manager.resetRunAccess.Lock()
				manager.resetRunAccess.Unlock()
				return notifiedCtx
			}
			testCase.notify(manager)
			first := waitNotification()
			// The callback intentionally waits until after ResetNetwork returns.
			// Returning from the dispatch goroutine must not cancel its work.
			require.Never(t, func() bool { return first.Err() != nil }, 100*time.Millisecond, time.Millisecond)
			listener.release <- struct{}{}
			select {
			case callbackErr := <-listener.result:
				require.NoError(t, callbackErr)
			case <-time.After(time.Second):
				t.Fatal("asynchronous reset callback did not finish")
			}

			testCase.notify(manager)
			second := waitNotification()
			require.ErrorIs(t, first.Err(), context.Canceled, "superseding update must cancel the previous listener context")
			require.NoError(t, second.Err())
			require.NoError(t, scope.Close())
			require.ErrorIs(t, second.Err(), context.Canceled, "scope teardown must cancel the latest listener context")
		})
	}
}
