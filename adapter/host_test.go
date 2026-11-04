package adapter

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/stretchr/testify/require"
)

type hostTestNetwork struct {
	NetworkManager
	port tun.RoutedPort
}

func (n hostTestNetwork) HostPort() tun.RoutedPort { return n.port }

type hostTestPort struct {
	tun.Port
	v4, v6 netip.Addr
}

func (*hostTestPort) RoutedPort()                               {}
func (p *hostTestPort) PortAddresses() (netip.Addr, netip.Addr) { return p.v4, p.v6 }

func TestJudgeHostFlow(t *testing.T) {
	p := &hostTestPort{v4: netip.MustParseAddr("172.19.0.1"), v6: netip.MustParseAddr("fd00::1")}
	for _, tt := range []struct {
		name        string
		port        tun.RoutedPort
		protocol    uint8
		destination string
		action      tun.FlowAction
		target      string
	}{
		{"tcp", p, 6, "100.64.0.1:443", tun.ActionFlow, "172.19.0.1:443"},
		{"udp6", p, 17, "[fd7a::1]:53", tun.ActionFlow, "[fd00::1]:53"},
		{"icmp", p, 1, "100.64.0.1:0", tun.ActionFlow, "172.19.0.1:0"},
		{"no tun", nil, 6, "100.64.0.1:443", tun.ActionAccept, ""},
		{"unsupported protocol", p, 99, "100.64.0.1:443", tun.ActionAccept, ""},
		{"missing family", &hostTestPort{v4: p.v4}, 17, "[fd7a::1]:53", tun.ActionAccept, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := JudgeHostFlow(context.Background(), hostTestNetwork{port: tt.port}, log.NewNOPFactory().Logger(), tt.protocol, netip.MustParseAddrPort("100.64.0.2:1234"), netip.MustParseAddrPort(tt.destination))
			require.Equal(t, tt.action, v.Action)
			if tt.action == tun.ActionFlow {
				require.Same(t, tt.port, v.Port)
				require.Equal(t, netip.MustParseAddrPort(tt.target), v.Destination)
				require.NotNil(t, v.NewTracker())
			}
		})
	}
}
