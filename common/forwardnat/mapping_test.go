package forwardnat_test

import (
	"net/netip"
	"testing"

	tun "github.com/sagernet/sing-tun"
	"github.com/stretchr/testify/require"
)

var client = netip.MustParseAddrPort("10.0.0.2:12345")
var server = netip.MustParseAddrPort("198.51.100.1:443")

func TestForwardNATMappingModes(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		mode                  tun.NATMapping
		samePort, sameAddress bool
	}{
		{"endpoint independent", tun.NATMappingEndpointIndependent, true, true},
		{"address dependent", tun.NATMappingAddressDependent, true, false},
		{"address and port dependent", tun.NATMappingAddressAndPortDependent, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newForwardFixture(t, tun.UDPNatOptions{Mapping: tc.mode})
			a, _ := packetEndpoints(f.send(client, server, "a"))
			b, _ := packetEndpoints(f.send(client, netip.MustParseAddrPort("198.51.100.1:444"), "b"))
			c, _ := packetEndpoints(f.send(client, netip.MustParseAddrPort("198.51.100.2:443"), "c"))
			require.Equal(t, tc.samePort, a == b)
			require.Equal(t, tc.sameAddress, a == c)
		})
	}
}

func TestForwardNATSeparatesClients(t *testing.T) {
	f := newForwardFixture(t, tun.UDPNatOptions{})
	other := netip.MustParseAddrPort("10.0.0.3:12345")
	a, _ := packetEndpoints(f.send(client, server, "a"))
	b, _ := packetEndpoints(f.send(other, server, "b"))
	require.NotEqual(t, a, b)
	for _, tc := range []struct {
		nat, original netip.AddrPort
		payload       string
	}{{a, client, "reply a"}, {b, other, "reply b"}} {
		before := len(f.writeback.packets)
		require.Empty(t, f.port.returnPath.ReturnPackets([][]byte{udpPacket(t, server, tc.nat, []byte(tc.payload))}))
		require.Len(t, f.writeback.packets, before+1)
		reply := f.writeback.packets[before]
		src, dst := packetEndpoints(reply)
		require.Equal(t, server, src)
		require.Equal(t, tc.original, dst)
		require.Equal(t, tc.payload, string(reply[28:]))
		assertUDPChecksum(t, reply)
	}
}

func TestForwardNATFilteringModes(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		mode                    tun.NATFiltering
		sameAddress, newAddress bool
	}{
		{"endpoint independent", tun.NATFilteringEndpointIndependent, true, true},
		{"address dependent", tun.NATFilteringAddressDependent, true, false},
		{"address and port dependent", tun.NATFilteringAddressAndPortDependent, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newForwardFixture(t, tun.UDPNatOptions{Filtering: tc.mode})
			nat, _ := packetEndpoints(f.send(client, server, "request"))
			for _, peer := range []struct {
				address string
				allowed bool
			}{{"198.51.100.1:443", true}, {"198.51.100.1:444", tc.sameAddress}, {"198.51.100.2:443", tc.newAddress}} {
				before := len(f.writeback.packets)
				left := f.port.returnPath.ReturnPackets([][]byte{udpPacket(t, netip.MustParseAddrPort(peer.address), nat, []byte("reply"))})
				if peer.allowed {
					require.Empty(t, left)
					require.Len(t, f.writeback.packets, before+1)
					_, dst := packetEndpoints(f.writeback.packets[before])
					require.Equal(t, client, dst)
					assertUDPChecksum(t, f.writeback.packets[before])
				} else {
					require.Len(t, left, 1)
					require.Len(t, f.writeback.packets, before)
				}
			}
		})
	}
}

func TestForwardNATSelectorExhaustion(t *testing.T) {
	f := newForwardFixture(t, tun.UDPNatOptions{})
	f.port.ranges = []tun.SelectorRange{{Start: 50000, Count: 2}}
	for _, n := range []uint16{50000, 50001} {
		f.port.occupied[netip.AddrPortFrom(netip.MustParseAddr("192.0.2.1"), n)] = true
	}
	require.True(t, f.dispatchNew(udpPacket(t, client, server, []byte("request"))))
	f.stage.Flush()
	require.Empty(t, f.port.packets)
	require.Empty(t, f.port.reserved)
}

func TestForwardNATReleasesReservations(t *testing.T) {
	f := newForwardFixture(t, tun.UDPNatOptions{})
	a, _ := packetEndpoints(f.send(client, server, "a"))
	b, _ := packetEndpoints(f.send(client, netip.MustParseAddrPort("198.51.100.2:443"), "b"))
	require.Equal(t, a, b)
	require.Len(t, f.port.reserved, 1)
	f.dispatcher.Close()
	require.Empty(t, f.port.reserved)
	require.Equal(t, []netip.AddrPort{a}, f.port.released)
}

func TestForwardNATNestedUpstreamCapability(t *testing.T) {
	f := newForwardFixture(t, tun.UDPNatOptions{})
	f.port.ranges = []tun.SelectorRange{{Start: 50000, Count: 2}}
	f.port.occupied[netip.MustParseAddrPort("192.0.2.1:50001")] = true
	inner := &fixtureWrapper{Port: f.port, upstream: f.port}
	f.handler.port = &fixtureWrapper{Port: inner, upstream: inner}
	nat, _ := packetEndpoints(f.send(client, server, "request"))
	require.Equal(t, netip.MustParseAddrPort("192.0.2.1:50000"), nat)
	f.dispatcher.Close()
	require.Empty(t, f.port.reserved)
}

func TestForwardNATRejectsLateReturnsAfterReset(t *testing.T) { testLateReturn(t, false) }
func TestForwardNATRejectsLateReturnsAfterClose(t *testing.T) { testLateReturn(t, true) }

func TestForwardNATRejectsLateReturnsIntoNewClientAfterReset(t *testing.T) {
	f := newForwardFixture(t, tun.UDPNatOptions{})
	f.port.ranges = []tun.SelectorRange{{Start: 50000, Count: 2}}
	oldClient := netip.MustParseAddrPort("10.0.0.2:50000")
	oldNAT, _ := packetEndpoints(f.send(oldClient, server, "old request"))
	returnPath := f.port.returnPath
	f.dispatcher.ResetNetwork()
	f.stage.Flush()
	newClient := netip.MustParseAddrPort("10.0.0.3:50000")
	newServer := netip.MustParseAddrPort("198.51.100.2:443")
	newNAT, _ := packetEndpoints(f.send(newClient, newServer, "new request"))
	// A previous-network reply must not cross
	// into another client's new session through endpoint-independent filtering.
	require.Empty(t, returnPath.ReturnPackets([][]byte{udpPacket(t, newServer, newNAT, []byte("current"))}))
	require.Len(t, f.writeback.packets, 1)
	returnPath.ReturnPackets([][]byte{udpPacket(t, server, oldNAT, []byte("late old reply"))})
	require.Len(t, f.writeback.packets, 1, "old-network reply was delivered into a new client's flow")
}

func TestForwardNATRepeatedResetExhaustionAndExpansion(t *testing.T) {
	for _, expand := range []bool{false, true} {
		f := newForwardFixture(t, tun.UDPNatOptions{})
		f.port.ranges = []tun.SelectorRange{{Start: 50000, Count: 1}}
		old, _ := packetEndpoints(f.send(netip.MustParseAddrPort("10.0.0.2:50000"), server, "old"))
		f.dispatcher.ResetNetwork()
		f.stage.Flush()
		f.port.expand = expand
		before := len(f.port.packets)
		require.True(t, f.dispatchNew(udpPacket(t, netip.MustParseAddrPort("10.0.0.3:50000"), server, []byte("new"))))
		f.stage.Flush()
		if expand {
			require.Len(t, f.port.packets, before+1)
			current, _ := packetEndpoints(f.port.packets[before])
			require.NotEqual(t, old, current)
			require.Empty(t, f.port.returnPath.ReturnPackets([][]byte{udpPacket(t, server, old, []byte("late"))}))
			require.Empty(t, f.writeback.packets)
			f.dispatcher.ResetNetwork()
			f.stage.Flush()
			require.True(t, f.dispatchNew(udpPacket(t, netip.MustParseAddrPort("10.0.0.4:50000"), server, []byte("third"))))
			f.stage.Flush()
			require.Len(t, f.port.packets, before+1, "both selectors quarantined")
		} else {
			require.Len(t, f.port.packets, before, "exhaustion must refuse, not reuse a stale selector")
		}
		f.dispatcher.Close()
		require.Empty(t, f.port.reserved)
	}
}
func testLateReturn(t *testing.T, closeDispatcher bool) {
	f := newForwardFixture(t, tun.UDPNatOptions{})
	nat, _ := packetEndpoints(f.send(client, server, "request"))
	returnPath := f.port.returnPath
	if closeDispatcher {
		f.dispatcher.Close()
	} else {
		f.dispatcher.ResetNetwork()
		f.stage.Flush()
	}
	returnPath.ReturnPackets([][]byte{udpPacket(t, server, nat, []byte("late"))})
	require.Empty(t, f.writeback.packets)
}
