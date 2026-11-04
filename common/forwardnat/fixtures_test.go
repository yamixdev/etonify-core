package forwardnat_test

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
	"github.com/stretchr/testify/require"
)

type forwardFixture struct {
	t          *testing.T
	dispatcher *tun.ForwardDispatcher
	stage      *tun.ForwardStage
	port       *fixturePort
	writeback  *packetCapture
	handler    *fixtureHandler
	judged     chan struct{}
}

func newForwardFixture(t *testing.T, options tun.UDPNatOptions) *forwardFixture {
	t.Helper()
	p := &fixturePort{ranges: []tun.SelectorRange{{Start: 50000, Count: 32}}, occupied: make(map[netip.AddrPort]bool), reserved: make(map[netip.AddrPort]bool)}
	w := &packetCapture{}
	h := &fixtureHandler{port: p}
	d := tun.NewForwardDispatcher(h, w, logger.NOP(), 0, options, time.Minute)
	judged := make(chan struct{}, 16)
	f := &forwardFixture{t: t, dispatcher: d, stage: d.NewStage(nil, func() { judged <- struct{}{} }), port: p, writeback: w, handler: h, judged: judged}
	t.Cleanup(d.Close)
	return f
}

func (f *forwardFixture) send(source, destination netip.AddrPort, payload string) []byte {
	f.t.Helper()
	before := len(f.port.packets)
	require.True(f.t, f.stage.Dispatch(udpPacket(f.t, source, destination, []byte(payload))))
	f.stage.Flush()
	for len(f.port.packets) == before {
		f.resumeJudged()
	}
	require.Len(f.t, f.port.packets, before+1)
	packet := f.port.packets[before]
	assertUDPChecksum(f.t, packet)
	return packet
}

func (f *forwardFixture) resumeJudged() {
	f.t.Helper()
	select {
	case <-f.judged:
	case <-time.After(2 * time.Second):
		f.t.Fatal("flow judgement did not complete")
	}
	f.stage.ResumeJudged(func([]byte, tun.ForwardFrameMeta) {
		f.t.Fatal("unexpected accept path in NAT fixture")
	})
	f.stage.Flush()
}

func (f *forwardFixture) dispatchNew(packet []byte) bool {
	f.t.Helper()
	handled := f.stage.Dispatch(packet)
	f.resumeJudged()
	return handled
}

type packetCapture struct{ packets [][]byte }

func (*packetCapture) ReturnHeadroom() int { return 0 }
func (p *packetCapture) WriteReturnPackets(packets [][]byte) error {
	for _, packet := range packets {
		p.packets = append(p.packets, append([]byte(nil), packet...))
	}
	return nil
}

type fixturePort struct {
	packetCapture
	returnPath         tun.Return
	ranges             []tun.SelectorRange
	occupied, reserved map[netip.AddrPort]bool
	released           []netip.AddrPort
	expand             bool
}

func (*fixturePort) PortAddresses() (netip.Addr, netip.Addr) {
	return netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::1")
}
func (*fixturePort) PortMTU() uint32                   { return 1500 }
func (p *fixturePort) AttachReturn(r tun.Return) error { p.returnPath = r; return nil }
func (p *fixturePort) DetachReturn(r tun.Return) error {
	if p.returnPath == r {
		p.returnPath = nil
	}
	return nil
}
func (p *fixturePort) WritePackets(packets [][]byte) error          { return p.WriteReturnPackets(packets) }
func (p *fixturePort) PortSelectorRanges(uint8) []tun.SelectorRange { return p.ranges }
func (p *fixturePort) ExpandSelectorRanges(uint8) bool {
	if !p.expand {
		return false
	}
	p.expand = false
	p.ranges = append(p.ranges, tun.SelectorRange{Start: 51000, Count: 1})
	return true
}
func (p *fixturePort) ReserveSelector(protocol uint8, address netip.AddrPort) bool {
	if protocol != 17 || p.occupied[address] || p.reserved[address] {
		return false
	}
	p.reserved[address] = true
	return true
}
func (p *fixturePort) ReleaseSelector(_ uint8, address netip.AddrPort) {
	delete(p.reserved, address)
	p.released = append(p.released, address)
}

// Embedding only Port deliberately hides the selector interfaces from this layer.
type fixtureWrapper struct {
	tun.Port
	upstream any
}

func (p *fixtureWrapper) UpstreamPort() any { return p.upstream }

type fixtureHandler struct {
	tun.Handler // Unexpected accept/DNS paths fail instead of being silently handled.
	port        tun.Port
	trackers    []*fixtureTracker
}

func (h *fixtureHandler) JudgeFlow(uint8, netip.AddrPort, netip.AddrPort, []byte) tun.FlowVerdict {
	return tun.FlowVerdict{Action: tun.ActionFlow, Port: h.port, NewTracker: func() tun.FlowTracker {
		tracker := &fixtureTracker{}
		h.trackers = append(h.trackers, tracker)
		return tracker
	}}
}

type fixtureTracker struct{ handle tun.FlowHandle }

func (t *fixtureTracker) AttachFlow(h tun.FlowHandle) { t.handle = h }
func (*fixtureTracker) CountForward(int)              {}
func (*fixtureTracker) CountReverse(int)              {}
func (*fixtureTracker) FlowEstablished()              {}
func (*fixtureTracker) CloseFlow(tun.FlowCloseReason) {}

func ipHeaderLength(packet []byte) int {
	if packet[0]>>4 == 6 {
		return 40
	}
	return int(packet[0]&15) * 4
}
func packetAddresses(packet []byte) (netip.Addr, netip.Addr) {
	if packet[0]>>4 == 6 {
		return netip.AddrFrom16([16]byte(packet[8:24])), netip.AddrFrom16([16]byte(packet[24:40]))
	}
	return netip.AddrFrom4([4]byte(packet[12:16])), netip.AddrFrom4([4]byte(packet[16:20]))
}
func packetEndpoints(packet []byte) (netip.AddrPort, netip.AddrPort) {
	s, d := packetAddresses(packet)
	udp := packet[ipHeaderLength(packet):]
	return netip.AddrPortFrom(s, binary.BigEndian.Uint16(udp)), netip.AddrPortFrom(d, binary.BigEndian.Uint16(udp[2:]))
}

// Independent RFC 1071 checksum, not sing-tun's rewrite/checksum helpers.
func internetChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) != 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}
func udpPseudoHeader(source, destination netip.Addr, length int) []byte {
	b := append(append([]byte(nil), source.AsSlice()...), destination.AsSlice()...)
	if source.Is4() {
		return append(b, 0, 17, byte(length>>8), byte(length))
	}
	return append(b, byte(length>>24), byte(length>>16), byte(length>>8), byte(length), 0, 0, 0, 17)
}
func udpPacket(t *testing.T, source, destination netip.AddrPort, payload []byte) []byte {
	t.Helper()
	require.Equal(t, source.Addr().Is4(), destination.Addr().Is4())
	udp := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(udp, source.Port())
	binary.BigEndian.PutUint16(udp[2:], destination.Port())
	binary.BigEndian.PutUint16(udp[4:], uint16(len(udp)))
	copy(udp[8:], payload)
	cs := internetChecksum(append(udpPseudoHeader(source.Addr(), destination.Addr(), len(udp)), udp...))
	if cs == 0 {
		cs = 65535
	}
	binary.BigEndian.PutUint16(udp[6:], cs)
	headerLength := 40
	if source.Addr().Is4() {
		headerLength = 20
	}
	packet := make([]byte, headerLength+len(udp))
	copy(packet[headerLength:], udp)
	if headerLength == 20 {
		packet[0], packet[8], packet[9] = 0x45, 64, 17
		binary.BigEndian.PutUint16(packet[2:], uint16(len(packet)))
		copy(packet[12:16], source.Addr().AsSlice())
		copy(packet[16:20], destination.Addr().AsSlice())
		binary.BigEndian.PutUint16(packet[10:], internetChecksum(packet[:20]))
	} else {
		packet[0], packet[6], packet[7] = 0x60, 17, 64
		binary.BigEndian.PutUint16(packet[4:], uint16(len(udp)))
		copy(packet[8:24], source.Addr().AsSlice())
		copy(packet[24:40], destination.Addr().AsSlice())
	}
	return packet
}
func assertUDPChecksum(t *testing.T, packet []byte) {
	t.Helper()
	h := ipHeaderLength(packet)
	s, d := packetAddresses(packet)
	if h == 20 {
		require.Zero(t, internetChecksum(packet[:h]), "IPv4 header checksum")
	}
	udp := packet[h:]
	require.Equal(t, len(udp), int(binary.BigEndian.Uint16(udp[4:])))
	require.Zero(t, internetChecksum(append(udpPseudoHeader(s, d, len(udp)), udp...)), "UDP checksum")
}
