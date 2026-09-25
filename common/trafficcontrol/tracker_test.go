package trafficcontrol

import (
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

type attributionOutbound struct {
	adapter.Outbound
	tag  string
	kind string
}

func (o attributionOutbound) Tag() string  { return o.tag }
func (o attributionOutbound) Type() string { return o.kind }

func TestTrackerUsesResolvedNetworkSpecificOutboundChain(t *testing.T) {
	group := attributionOutbound{tag: "automatic", kind: "urltest"}
	leaf := attributionOutbound{tag: "udp-leaf", kind: "hysteria2"}
	metadata := adapter.InboundContext{
		OutboundChain: []adapter.Outbound{group, leaf},
	}
	result := new(Manager).newTrackerMetadata(metadata, nil, group, new(atomic.Int64), new(atomic.Int64))
	require.Equal(t, []string{"udp-leaf", "automatic"}, result.Chain)
	require.Equal(t, "udp-leaf", result.Outbound)
	require.Equal(t, "hysteria2", result.OutboundType)
}
