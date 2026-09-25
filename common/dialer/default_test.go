package dialer

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type attributionTestOutbound struct {
	adapter.Outbound
	tag string
}

func (o attributionTestOutbound) Tag() string { return o.tag }

func TestDialAttributionUsesResolvedOutboundChain(t *testing.T) {
	metadata := &adapter.InboundContext{
		RouteOutbound: "automatic",
		OutboundChain: []adapter.Outbound{
			attributionTestOutbound{tag: "automatic"},
			attributionTestOutbound{tag: "udp-leaf"},
		},
	}
	ctx := adapter.WithContext(context.Background(), metadata)
	result := new(DefaultDialer).dialAttribution(ctx, M.Socksaddr{})
	require.Equal(t, []string{"udp-leaf", "automatic"}, result.Chain)
}
