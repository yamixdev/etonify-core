package rootmode

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type failingCloser struct{}

func (failingCloser) Close() error { return Reason("cleanup_failed") }
func TestLeaseReportsCleanupFailure(t *testing.T) {
	var output bytes.Buffer
	err := ServeLease(context.Background(), strings.NewReader("{}\n"), &output, func(context.Context, []byte) (io.Closer, error) { return failingCloser{}, nil })
	if err == nil || err.Error() != "cleanup_failed" {
		t.Fatalf("lost cleanup failure: %v", err)
	}
}
