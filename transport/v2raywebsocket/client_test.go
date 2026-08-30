package v2raywebsocket

import (
	"context"
	"net"
	"net/url"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

type handshakeFailureDialer struct{}

func (handshakeFailureDialer) DialContext(
	context.Context,
	string,
	M.Socksaddr,
) (net.Conn, error) {
	clientConn, serverConn := net.Pipe()
	go serverConn.Close()
	return clientConn, nil
}

func (handshakeFailureDialer) ListenPacket(
	context.Context,
	M.Socksaddr,
) (net.PacketConn, error) {
	panic("unexpected ListenPacket call")
}

func TestDialContextHandshakeFailureReturnsNilConnection(t *testing.T) {
	client := &Client{
		dialer: handshakeFailureDialer{},
		requestURL: url.URL{
			Scheme: "ws",
			Host:   "example.com",
			Path:   "/",
		},
	}

	conn, err := client.DialContext(context.Background())
	if err == nil {
		t.Fatal("expected WebSocket handshake error")
	}
	if conn != nil {
		t.Fatalf("expected nil connection on handshake failure, got %T", conn)
	}
}
