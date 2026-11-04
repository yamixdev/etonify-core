package forwardnat_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
	"github.com/stretchr/testify/require"
)

type routedFixturePort struct{ fixturePort }

func (*routedFixturePort) RoutedPort() {}

type hostNetwork struct {
	adapter.NetworkManager
	port tun.RoutedPort
}

func (n hostNetwork) HostPort() tun.RoutedPort { return n.port }

type hostHandler struct {
	tun.Handler
	network hostNetwork
}

func (h hostHandler) JudgeFlow(protocol uint8, source, destination netip.AddrPort, _ []byte) tun.FlowVerdict {
	return adapter.JudgeHostFlow(context.Background(), h.network, log.NewNOPFactory().Logger(), protocol, source, destination)
}

func TestEndpointHostFlowPreservesSourceAndReturnsToEndpoint(t *testing.T) {
	for _, tt := range []struct{ source, destination, target string }{
		{"100.64.0.2:12345", "100.64.0.1:443", "192.0.2.1:443"},
		{"[fd7a::2]:12345", "[fd7a::1]:443", "[2001:db8::1]:443"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			p := &routedFixturePort{fixturePort: fixturePort{ranges: []tun.SelectorRange{{Start: 12345, Count: 1}}}}
			w := &packetCapture{}
			d := tun.NewForwardDispatcher(hostHandler{network: hostNetwork{port: p}}, w, logger.NOP(), 1500, tun.UDPNatOptions{}, time.Minute)
			t.Cleanup(d.Close)
			judged := make(chan struct{}, 1)
			s := d.NewStage(nil, func() { judged <- struct{}{} })
			source := netip.MustParseAddrPort(tt.source)
			destination := netip.MustParseAddrPort(tt.destination)
			require.True(t, s.Dispatch(udpPacket(t, source, destination, []byte("request"))))
			select {
			case <-judged:
			case <-time.After(time.Second):
				t.Fatal("flow judgement timed out")
			}
			s.ResumeJudged(func([]byte, tun.ForwardFrameMeta) { t.Fatal("host flow accepted instead of forwarded") })
			s.Flush()
			require.Len(t, p.packets, 1)
			forwardSource, forwardDestination := packetEndpoints(p.packets[0])
			require.Equal(t, source, forwardSource)
			require.Equal(t, netip.MustParseAddrPort(tt.target), forwardDestination)
			assertUDPChecksum(t, p.packets[0])
			require.Empty(t, p.returnPath.ReturnPackets([][]byte{udpPacket(t, forwardDestination, forwardSource, []byte("response"))}))
			require.Len(t, w.packets, 1)
			responseSource, responseDestination := packetEndpoints(w.packets[0])
			require.Equal(t, destination, responseSource)
			require.Equal(t, source, responseDestination)
			assertUDPChecksum(t, w.packets[0])
			d.ResetNetwork()
			s.Flush()
			p.returnPath.ReturnPackets([][]byte{udpPacket(t, forwardDestination, forwardSource, []byte("late"))})
			require.Len(t, w.packets, 1, "late host response crossed network reset")
		})
	}
}
