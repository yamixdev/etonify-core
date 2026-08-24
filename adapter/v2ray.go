package adapter

import (
	"context"
	"net"

	N "github.com/sagernet/sing/common/network"
)

type V2RayServerTransport interface {
	Network() []string
	Serve(listener net.Listener) error
	ServePacket(listener net.PacketConn) error
	Close() error
}

type V2RayServerTransportHandler interface {
	N.TCPConnectionHandlerEx
}

type V2RayClientTransport interface {
	DialContext(ctx context.Context) (net.Conn, error)
	Close() error
}

// V2RayClientTransportResetter is implemented by transports that can discard
// network-bound state without becoming permanently closed.
//
// Interface updates happen during handover between Wi-Fi and mobile networks.
// Most transports must be recreated after such an update, but XHTTP can close
// its old requests and connection pools while remaining usable for the next
// dial.
type V2RayClientTransportResetter interface {
	Reset()
}

// ResetV2RayClientTransport resets a transport when it supports handover.
// Older transports keep the previous behaviour and are closed instead.
func ResetV2RayClientTransport(transport V2RayClientTransport) {
	if transport == nil {
		return
	}
	if resetter, isResettable := transport.(V2RayClientTransportResetter); isResettable {
		resetter.Reset()
		return
	}
	_ = transport.Close()
}
