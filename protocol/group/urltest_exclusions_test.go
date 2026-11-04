package group

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	U "github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func TestURLTestExclusionsPreserveAttachedFlows(t *testing.T) {
	a, b := &urlTestSelectionOutbound{tag: "a"}, &urlTestSelectionOutbound{tag: "b"}
	h := U.NewHistoryStorage()
	g := &URLTestGroup{outbounds: []adapter.Outbound{a, b}, history: h, interruptGroup: interrupt.NewGroup(), selectedOutboundTCP: a, selectedOutboundUDP: a}
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	remove := g.interruptGroup.Add(client, false)
	defer remove()
	h.SetExcludedTags([]string{"a"})
	g.RefreshEligibility()
	selected, _ := g.Select(N.NetworkTCP)
	require.Same(t, b, selected)
	require.NoError(t, client.SetDeadline(time.Time{}))
	h.SetExcludedTags([]string{"a", "b"})
	g.RefreshEligibility()
	selected, _ = g.Select(N.NetworkTCP)
	require.Nil(t, selected)
	require.Nil(t, g.selectedOutbound(N.NetworkTCP))
	endpoint := &URLTest{group: g}
	_, err := endpoint.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("example.test:443"))
	require.ErrorContains(t, err, "missing supported outbound")
	_, err = endpoint.ListenPacket(context.Background(), M.ParseSocksaddr("example.test:53"))
	require.ErrorContains(t, err, "missing supported outbound")
	require.NoError(t, client.SetDeadline(time.Time{}))
	h.SetExcludedTags(nil)
	g.RefreshEligibility()
	selected, _ = g.Select(N.NetworkTCP)
	require.NotNil(t, selected)
}

type exclusionNestedGroup struct {
	adapter.Outbound
	selected adapter.Outbound
}

func (g exclusionNestedGroup) All() []string                     { return []string{g.selected.Tag()} }
func (g exclusionNestedGroup) Selected(string) adapter.Outbound  { return g.selected }
func (g exclusionNestedGroup) AttachConnection(io.Closer) func() { return func() {} }

func TestURLTestExcludesNestedSelectedLeafWithoutChangingManualGroup(t *testing.T) {
	a, b := &urlTestSelectionOutbound{tag: "a"}, &urlTestSelectionOutbound{tag: "b"}
	manual := exclusionNestedGroup{Outbound: &urlTestSelectionOutbound{tag: "manual"}, selected: a}
	h := U.NewHistoryStorage()
	h.SetExcludedTags([]string{"a"})
	g := &URLTestGroup{outbounds: []adapter.Outbound{manual, b}, history: h, selectedOutboundTCP: manual}
	selected, _ := g.Select(N.NetworkTCP)
	require.Same(t, b, selected)
	require.Same(t, a, manual.Selected(N.NetworkTCP))
}

func TestURLTestExcludedLeafIsNotProbed(t *testing.T) {
	var attempts atomic.Int32
	a := &urlTestSelectionOutbound{tag: "a", dialAttempts: &attempts}
	h := U.NewHistoryStorage()
	h.SetExcludedTags([]string{"a"})
	result := URLTestOutbounds(context.Background(), nil, h, log.NewNOPFactory().Logger(), []adapter.Outbound{a}, "https://example.test", time.Second, true)
	require.Empty(t, result)
	require.Zero(t, attempts.Load())
}
