package rootmode

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestLeaseCancellationBeforeConfigNeverStartsRuntime(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeLease(ctx, input, io.Discard, func(context.Context, []byte) (io.Closer, error) {
			t.Error("started after cancellation")
			return nil, nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cancelled lease blocked waiting for config")
	}
}
