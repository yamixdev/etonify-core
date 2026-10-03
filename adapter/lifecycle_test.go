package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
)

func TestScopeLateCleanupAfterCloseRunsOnce(t *testing.T) {
	scope := NewScope(context.Background(), log.NewNOPFactory().Logger())
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	var cleaned atomic.Int32
	scope.Add(func() error {
		cleaned.Add(1)
		return nil
	})
	if got := cleaned.Load(); got != 1 {
		t.Fatalf("late resource was not closed: cleanups=%d", got)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	if got := cleaned.Load(); got != 1 {
		t.Fatalf("resource closed more than once: cleanups=%d", got)
	}
}

type blockedScopeStart struct {
	entered chan struct{}
	resume  chan struct{}
	cleaned atomic.Int32
}

func (c *blockedScopeStart) Start(_ StartStage, scope *Scope) error {
	close(c.entered)
	<-c.resume
	scope.Add(func() error {
		c.cleaned.Add(1)
		return nil
	})
	return nil
}

func TestScopeInFlightStartCleansAfterClose(t *testing.T) {
	scope := NewScope(context.Background(), log.NewNOPFactory().Logger())
	component := &blockedScopeStart{entered: make(chan struct{}), resume: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- scope.Start("blocked resource", component, StartStateStart) }()
	select {
	case <-component.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("resource startup did not enter")
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	close(component.resume)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("closed scope reported successful startup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup remained blocked after close")
	}
	if got := component.cleaned.Load(); got != 1 {
		t.Fatalf("resource registered during close leaked: cleanups=%d", got)
	}
}

func TestScopeLateCleanupCanRegisterAnotherCleanup(t *testing.T) {
	scope := NewScope(context.Background(), log.NewNOPFactory().Logger())
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	var cleaned atomic.Int32
	done := make(chan struct{})
	go func() {
		scope.Add(func() error {
			scope.Add(func() error { cleaned.Add(1); return nil })
			cleaned.Add(1)
			return nil
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup deadlocked on scope lock")
	}
	if got := cleaned.Load(); got != 2 {
		t.Fatalf("nested late cleanup leaked: cleanups=%d", got)
	}
}
