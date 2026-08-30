package group

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

type realTagTestManager struct {
	adapter.OutboundManager
	items map[string]adapter.Outbound
}

func (m *realTagTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.items[tag]
	return outbound, loaded
}

type realTagTestOutbound struct {
	adapter.Outbound
	tag string
}

func (o *realTagTestOutbound) Tag() string { return o.tag }

type realTagTestGroup struct {
	adapter.Outbound
	tag string
	now string
}

func (g *realTagTestGroup) Tag() string   { return g.tag }
func (g *realTagTestGroup) Now() string   { return g.now }
func (g *realTagTestGroup) All() []string { return []string{g.now} }

func TestRealTagResolvesNestedGroups(t *testing.T) {
	t.Parallel()

	leaf := &realTagTestOutbound{tag: "leaf"}
	inner := &realTagTestGroup{tag: "inner", now: leaf.tag}
	outer := &realTagTestGroup{tag: "outer", now: inner.tag}
	manager := &realTagTestManager{items: map[string]adapter.Outbound{
		leaf.tag:  leaf,
		inner.tag: inner,
		outer.tag: outer,
	}}

	require.Equal(t, leaf.tag, RealTag(manager, outer))
}

func TestRealTagReturnsMissingSelectedTag(t *testing.T) {
	t.Parallel()

	group := &realTagTestGroup{tag: "group", now: "missing"}
	manager := &realTagTestManager{items: map[string]adapter.Outbound{
		group.tag: group,
	}}

	require.Equal(t, "missing", RealTag(manager, group))
}
