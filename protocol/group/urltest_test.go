package group

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	U "github.com/sagernet/sing-box/common/urltest"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

type urlTestSelectionOutbound struct {
	tag string
}

func (o *urlTestSelectionOutbound) Type() string           { return "test" }
func (o *urlTestSelectionOutbound) Tag() string            { return o.tag }
func (o *urlTestSelectionOutbound) Network() []string      { return []string{N.NetworkTCP, N.NetworkUDP} }
func (o *urlTestSelectionOutbound) Dependencies() []string { return nil }
func (o *urlTestSelectionOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
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
