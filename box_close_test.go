package box

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

func TestBoxBeginCloseElectsSingleOwner(t *testing.T) {
	box := &Box{}
	const callers = 64

	start := make(chan struct{})
	results := make(chan bool, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			results <- box.beginClose()
		}()
	}

	close(start)
	workers.Wait()
	close(results)

	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected one Close owner, got %d", winners)
	}
}

func TestBoxCloseRunsScopeCleanupOnce(t *testing.T) {
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	var cleanups atomic.Int32
	scope.Add(func() error { cleanups.Add(1); return nil })
	box := &Box{scope: scope, done: make(chan struct{})}
	const callers = 64
	results := make(chan error, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() { defer workers.Done(); results <- box.Close() }()
	}
	workers.Wait()
	close(results)
	closed := 0
	for err := range results {
		if errors.Is(err, os.ErrClosed) {
			closed++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if closed != callers-1 || cleanups.Load() != 1 {
		t.Fatalf("expected one cleanup owner: closed=%d cleanups=%d", closed, cleanups.Load())
	}
	if scope.Context().Err() != context.Canceled {
		t.Fatal("box close did not cancel the lifecycle scope")
	}
}
