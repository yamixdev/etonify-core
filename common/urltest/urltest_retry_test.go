package urltest

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/common/probe"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

var errRetrySafeCompatibility = errors.New("retry-safe compatibility failure")

type retryTestController struct {
	nextCalls    atomic.Int32
	successCalls atomic.Int32
	failureCalls atomic.Int32
}

func (c *retryTestController) Next(err error) bool {
	c.nextCalls.Add(1)
	return errors.Is(err, errRetrySafeCompatibility)
}

func (c *retryTestController) Success() {
	c.successCalls.Add(1)
}

func (c *retryTestController) Failure(error) {
	c.failureCalls.Add(1)
}

type retryTestDialer struct {
	controller *retryTestController
	dialCalls  atomic.Int32
}

func (d *retryTestDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	session := probe.URLTestSessionFromContext(ctx)
	if session == nil {
		return nil, errors.New("URLTest session is missing")
	}
	if d.dialCalls.Add(1) == 1 {
		if !session.BindController(d.controller) {
			return nil, errors.New("compatibility controller was not bound")
		}
		return nil, errRetrySafeCompatibility
	}
	clientConn, serverConn := net.Pipe()
	go serveURLTestResponse(serverConn)
	return clientConn, nil
}

func (d *retryTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("packet listening is not used by URLTest")
}

func serveURLTestResponse(conn net.Conn) {
	defer conn.Close()
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	_ = request.Body.Close()
	_, _ = conn.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
}

func TestURLTestRetriesBoundCompatibilityCandidateAndCommitsOnSuccess(t *testing.T) {
	t.Parallel()

	controller := new(retryTestController)
	dialer := &retryTestDialer{controller: controller}
	delay, err := URLTest(context.Background(), "http://example.com/generate_204", dialer)
	require.NoError(t, err)
	require.LessOrEqual(t, delay, uint16(1000))
	require.Equal(t, int32(2), dialer.dialCalls.Load())
	require.Equal(t, int32(1), controller.nextCalls.Load())
	require.Equal(t, int32(1), controller.successCalls.Load())
	require.Zero(t, controller.failureCalls.Load())
}
