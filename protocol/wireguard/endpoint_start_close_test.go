package wireguard

import (
	"errors"
	"os"
	"testing"

	"github.com/sagernet/sing-box/adapter"
)

func TestEndpointStartRefusesPendingClose(t *testing.T) {
	endpoint := &Endpoint{}
	endpoint.lifecycleAccess.Lock()

	result := make(chan error, 1)
	go func() {
		result <- endpoint.Start(adapter.StartStateStart)
	}()

	if !endpoint.beginClose() {
		t.Fatal("first Close must own endpoint shutdown")
	}
	endpoint.lifecycleAccess.Unlock()

	if err := <-result; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Start while Close is pending: expected os.ErrClosed, got %v", err)
	}
	if endpoint.beginClose() {
		t.Fatal("a second Close must not own endpoint shutdown")
	}
}
