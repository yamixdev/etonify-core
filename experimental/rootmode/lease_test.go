package rootmode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type testCloser struct{ closes *int }

func (c testCloser) Close() error { *c.closes++; return nil }

func TestLeaseStartsBeforeReadyAndStopsOnEOF(t *testing.T) {
	var output bytes.Buffer
	closes := 0
	err := ServeLease(context.Background(), strings.NewReader("{}\n"), &output, func(ctx context.Context, data []byte) (io.Closer, error) {
		if output.Len() != 0 || string(data) != "{}" {
			t.Fatal("ready before started")
		}
		return testCloser{&closes}, nil
	})
	if err != nil || closes != 1 || output.String() != "{\"ready\":true,\"protocol\":1}\n" {
		t.Fatalf("err=%v close=%d out=%s", err, closes, &output)
	}
}

func TestLeaseStartFailureNeverPublishesReadyOrSecrets(t *testing.T) {
	var output bytes.Buffer
	err := ServeLease(context.Background(), strings.NewReader("{}\n"), &output, func(context.Context, []byte) (io.Closer, error) { return nil, errors.New("secret configuration") })
	if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "secret") {
		t.Fatal("failure leaked ready/config")
	}
}

func TestLeaseCancellationClosesRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inputR, inputW := io.Pipe()
	defer inputR.Close()
	defer inputW.Close()
	go func() { io.WriteString(inputW, "{}\n") }()
	closes := 0
	var output bytes.Buffer
	err := ServeLease(ctx, inputR, &output, func(context.Context, []byte) (io.Closer, error) { cancel(); return testCloser{&closes}, nil })
	if err != nil || closes != 1 {
		t.Fatalf("runtime remained alive: %v %d", err, closes)
	}
}
