package route

import (
	"io"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

type routeTestOutbound struct {
	adapter.Outbound
	tag      string
	networks []string
}

func (o *routeTestOutbound) Tag() string       { return o.tag }
func (o *routeTestOutbound) Network() []string { return o.networks }

type routeTestGroup struct {
	*routeTestOutbound
	tcp      adapter.Outbound
	udp      adapter.Outbound
	attached []io.Closer
	onAttach func()
	failed   []string
}

type routeTestCloser struct{ closed int }

type routeTestFlowHandle struct{ closed int }

func (h *routeTestFlowHandle) CloseFlow() { h.closed++ }

func (c *routeTestCloser) Close() error {
	c.closed++
	return nil
}

func (g *routeTestGroup) Now() string   { return g.tcp.Tag() }
func (g *routeTestGroup) All() []string { return nil }
func (g *routeTestGroup) Selected(network string) adapter.Outbound {
	if network == N.NetworkUDP {
		return g.udp
	}
	return g.tcp
}
func (g *routeTestGroup) AttachConnection(closer io.Closer) func() {
	if g.onAttach != nil {
		g.onAttach()
	}
	g.attached = append(g.attached, closer)
	return func() { g.attached = nil }
}

func (g *routeTestGroup) Interrupt() {
	for _, closer := range g.attached {
		_ = closer.Close()
	}
}

func (g *routeTestGroup) SelectedOutboundFailed(network string, selected adapter.Outbound) {
	g.failed = append(g.failed, network+":"+selected.Tag())
}

func TestResolveOutboundFollowsNestedGroupsPerNetwork(t *testing.T) {
	tcp := &routeTestOutbound{tag: "tcp-node", networks: []string{N.NetworkTCP}}
	udp := &routeTestOutbound{tag: "udp-node", networks: []string{N.NetworkUDP}}
	inner := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "fastest"}, tcp: tcp, udp: udp}
	outer := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "selected"}, tcp: inner, udp: inner}

	tcpChain, err := resolveOutbound(outer, N.NetworkTCP)
	require.NoError(t, err)
	require.Equal(t, []adapter.Outbound{outer, inner, tcp}, tcpChain)

	udpChain, err := resolveOutbound(outer, N.NetworkUDP)
	require.NoError(t, err)
	require.Equal(t, []adapter.Outbound{outer, inner, udp}, udpChain)
}

func TestResolveOutboundRejectsUnsupportedNestedSelection(t *testing.T) {
	tcp := &routeTestOutbound{tag: "tcp-node", networks: []string{N.NetworkTCP}}
	group := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "selected"}, tcp: tcp}
	_, err := resolveOutbound(group, N.NetworkUDP)
	require.ErrorContains(t, err, "UDP is not supported")
}

func TestRegisterInterruptAttachesEveryNestedGroupAndDetachesOnClose(t *testing.T) {
	leaf := &routeTestOutbound{tag: "leaf", networks: []string{N.NetworkTCP}}
	inner := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "inner"}, tcp: leaf}
	outer := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "outer"}, tcp: inner}
	closer := &routeTestCloser{}
	called := false
	onClose, err := registerInterrupt([]adapter.Outbound{outer, inner, leaf}, N.NetworkTCP, closer, func(error) { called = true })
	require.NoError(t, err)
	require.Len(t, outer.attached, 1)
	require.Len(t, inner.attached, 1)

	inner.Interrupt()
	require.Equal(t, 1, closer.closed)
	onClose(nil)
	require.True(t, called)
	outer.Interrupt()
	require.Equal(t, 1, closer.closed)
}

func TestRegisterInterruptRejectsSelectionChangedBeforeAttachment(t *testing.T) {
	oldLeaf := &routeTestOutbound{tag: "old", networks: []string{N.NetworkTCP}}
	newLeaf := &routeTestOutbound{tag: "new", networks: []string{N.NetworkTCP}}
	group := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "selected"}, tcp: oldLeaf}
	group.onAttach = func() {
		group.tcp = newLeaf
		group.Interrupt()
	}
	closer := &routeTestCloser{}
	_, err := registerInterrupt([]adapter.Outbound{group, oldLeaf}, N.NetworkTCP, closer, nil)
	require.ErrorContains(t, err, "changed")
	require.Empty(t, group.attached)
}

func TestReportOutboundDialFailureNotifiesNestedGroups(t *testing.T) {
	leaf := &routeTestOutbound{tag: "failed", networks: []string{N.NetworkUDP}}
	inner := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "inner"}, udp: leaf}
	outer := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "outer"}, udp: inner}
	reportOutboundDialFailure([]adapter.Outbound{outer, inner, leaf}, N.NetworkUDP)
	require.Equal(t, []string{"udp:failed"}, outer.failed)
	require.Equal(t, []string{"udp:failed"}, inner.failed)
}

func TestFlowInterrupterClosesNestedGroupFlowOnSwitch(t *testing.T) {
	leaf := &routeTestOutbound{tag: "leaf", networks: []string{N.NetworkTCP}}
	inner := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "inner"}, tcp: leaf}
	outer := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "outer"}, tcp: inner}
	tracker := newFlowInterrupter([]adapter.Outbound{outer, inner, leaf}, N.NetworkTCP)
	require.NotNil(t, tracker)
	handle := &routeTestFlowHandle{}
	tracker.AttachFlow(handle)
	inner.Interrupt()
	require.Equal(t, 1, handle.closed)
	tracker.CloseFlow(tun.FlowCloseFinished)
	outer.Interrupt()
	require.Equal(t, 1, handle.closed)
}

func TestFlowInterrupterClosesFlowWhenSelectionChangesDuringAttach(t *testing.T) {
	oldLeaf := &routeTestOutbound{tag: "old", networks: []string{N.NetworkTCP}}
	newLeaf := &routeTestOutbound{tag: "new", networks: []string{N.NetworkTCP}}
	group := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "selected"}, tcp: oldLeaf}
	group.onAttach = func() {
		group.tcp = newLeaf
		group.Interrupt()
	}
	tracker := newFlowInterrupter([]adapter.Outbound{group, oldLeaf}, N.NetworkTCP)
	handle := &routeTestFlowHandle{}
	tracker.AttachFlow(handle)
	require.Equal(t, 1, handle.closed)
	require.Empty(t, group.attached)
}
