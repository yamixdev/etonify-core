package route

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

type handshakeFailPacketConn struct {
	N.PacketConn
	closed bool
}

func (c *handshakeFailPacketConn) PacketConnHandshakeSuccess(net.PacketConn) error {
	return errors.New("handshake rejected")
}

func (c *handshakeFailPacketConn) Close() error {
	c.closed = true
	return nil
}

type closeTrackingPacketConn struct {
	net.PacketConn
	closed bool
}

func (c *closeTrackingPacketConn) Close() error {
	c.closed = true
	return nil
}

type handshakeTestDialer struct {
	N.Dialer
	remote *closeTrackingPacketConn
}

type failedTCPDialer struct{ N.Dialer }

func (failedTCPDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("dial failed")
}

type failedUDPListener struct{ N.Dialer }

func (failedUDPListener) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("listen failed")
}

type closeTrackingConn struct {
	net.Conn
	closed bool
}

func (c *closeTrackingConn) Close() error {
	c.closed = true
	return nil
}

func (d handshakeTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return d.remote, nil
}

func TestPacketHandshakeFailureClosesAndNotifies(t *testing.T) {
	local := &handshakeFailPacketConn{}
	remote := &closeTrackingPacketConn{}
	var closedErr error
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.NewPacketConnection(context.Background(), handshakeTestDialer{remote: remote}, local, adapter.InboundContext{}, func(err error) {
		closedErr = err
	})
	require.ErrorContains(t, closedErr, "handshake rejected")
	require.True(t, local.closed)
	require.True(t, remote.closed)
}

func TestFailedTCPDialNotifiesSelectedGroup(t *testing.T) {
	leaf := &routeTestOutbound{tag: "failed", networks: []string{N.NetworkTCP}}
	group := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "automatic"}, tcp: leaf}
	local := &closeTrackingConn{}
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.NewConnection(context.Background(), failedTCPDialer{}, local, adapter.InboundContext{
		OutboundChain: []adapter.Outbound{group, leaf},
	}, nil)
	require.True(t, local.closed)
	require.Equal(t, []string{"tcp:failed"}, group.failed)
}

func TestFailedUDPListenNotifiesSelectedGroup(t *testing.T) {
	leaf := &routeTestOutbound{tag: "failed", networks: []string{N.NetworkUDP}}
	group := &routeTestGroup{routeTestOutbound: &routeTestOutbound{tag: "automatic"}, udp: leaf}
	local := &handshakeFailPacketConn{}
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.NewPacketConnection(context.Background(), failedUDPListener{}, local, adapter.InboundContext{
		OutboundChain: []adapter.Outbound{group, leaf},
	}, nil)
	require.True(t, local.closed)
	require.Equal(t, []string{"udp:failed"}, group.failed)
}
