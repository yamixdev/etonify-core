package group

import (
	"io"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

type realTagTestOutbound struct {
	adapter.Outbound
	tag string
}

func (o *realTagTestOutbound) Tag() string       { return o.tag }
func (o *realTagTestOutbound) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

type realTagTestGroup struct {
	*realTagTestOutbound
	tcp adapter.Outbound
	udp adapter.Outbound
}

func (g *realTagTestGroup) Now() string                       { return g.tcp.Tag() }
func (g *realTagTestGroup) All() []string                     { return nil }
func (g *realTagTestGroup) AttachConnection(io.Closer) func() { return func() {} }
func (g *realTagTestGroup) Selected(network string) adapter.Outbound {
	if network == N.NetworkUDP {
		return g.udp
	}
	return g.tcp
}

func TestRealTagResolvesNestedGroupForEachNetwork(t *testing.T) {
	tcp := &realTagTestOutbound{tag: "tcp-leaf"}
	udp := &realTagTestOutbound{tag: "udp-leaf"}
	inner := &realTagTestGroup{realTagTestOutbound: &realTagTestOutbound{tag: "fastest"}, tcp: tcp, udp: udp}
	outer := &realTagTestGroup{realTagTestOutbound: &realTagTestOutbound{tag: "selected"}, tcp: inner, udp: inner}
	require.Equal(t, "tcp-leaf", RealTag(outer, N.NetworkTCP))
	require.Equal(t, "udp-leaf", RealTag(outer, N.NetworkUDP))
}

func TestURLTestFailureInvalidatesSelectedLeafAndChoosesNext(t *testing.T) {
	failed := &realTagTestOutbound{tag: "failed"}
	working := &realTagTestOutbound{tag: "working"}
	history := urltest.NewHistoryStorage()
	history.StoreURLTestHistory(failed.Tag(), &adapter.URLTestHistory{Delay: 10})
	history.StoreURLTestHistory(working.Tag(), &adapter.URLTestHistory{Delay: 50})
	group := &URLTestGroup{
		history:        history,
		outbounds:      []adapter.Outbound{failed, working},
		interruptGroup: interrupt.NewGroup(),
	}
	group.performUpdateCheck()
	require.Equal(t, failed, group.selectedOutbound(N.NetworkTCP))
	urlTest := &URLTest{group: group}
	urlTest.SelectedOutboundFailed(N.NetworkTCP, failed)
	require.Nil(t, history.LoadCurrentURLTestHistory(failed.Tag()))
	require.Equal(t, working, group.selectedOutbound(N.NetworkTCP))
}
