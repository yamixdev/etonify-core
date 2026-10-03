package wireguard

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

func TestEndpointStartRefusesPendingClose(t *testing.T) {
	endpoint := &Endpoint{}
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() {
		if err := scope.Close(); err != nil {
			t.Errorf("close scope: %v", err)
		}
	})
	endpoint.lifecycleAccess.Lock()

	result := make(chan error, 1)
	go func() {
		result <- endpoint.Start(adapter.StartStateStart, scope)
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
