package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestCloseInstanceClosesRuntimeBeforeCancel(t *testing.T) {
	ctx, contextCancel := context.WithCancel(context.Background())
	closeErr := errors.New("close runtime")
	var events []string

	err := closeInstance(
		func() error {
			events = append(events, "history")
			return nil
		},
		func() error {
			select {
			case <-ctx.Done():
				t.Fatal("runtime context was canceled before the runtime closed")
			default:
			}
			events = append(events, "runtime")
			return closeErr
		},
		func() {
			events = append(events, "cancel")
			contextCancel()
		},
	)

	if !errors.Is(err, closeErr) {
		t.Fatalf("closeInstance() error = %v, want %v", err, closeErr)
	}
	if want := []string{"history", "runtime", "cancel"}; !slices.Equal(events, want) {
		t.Fatalf("close order = %v, want %v", events, want)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v, want %v", ctx.Err(), context.Canceled)
	}
}
