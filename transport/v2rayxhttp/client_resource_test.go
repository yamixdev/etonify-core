package v2rayxhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const xhttpResourceSoakIterations = 64

func TestXHTTPResourceSoak(t *testing.T) {
	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		started <- struct{}{}
		<-request.Context().Done()
		stopped <- struct{}{}
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(context.Background(), N.SystemDialer, M.ParseSocksaddr(parsedURL.Host), option.V2RayXHTTPOptions{
		Path: "/resource-soak",
		Mode: "packet-up",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	baselineFDs := platformOpenFileDescriptorCount(t)
	baselineGoroutines := runtime.NumGoroutine()
	for index := 0; index < xhttpResourceSoakIterations; index++ {
		connection, dialErr := client.DialContext(context.Background())
		if dialErr != nil {
			t.Fatalf("dial %d: %v", index, dialErr)
		}
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("download %d did not start", index)
		}
		if closeErr := connection.Close(); closeErr != nil {
			t.Fatalf("close %d: %v", index, closeErr)
		}
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Fatalf("download %d did not stop", index)
		}
		client.stateAccess.Lock()
		activeSessions := len(client.sessions)
		client.stateAccess.Unlock()
		if activeSessions != 0 {
			t.Fatalf("iteration %d left %d active sessions", index, activeSessions)
		}
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}

	const tolerance = 6
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		currentFDs := platformOpenFileDescriptorCount(t)
		currentGoroutines := runtime.NumGoroutine()
		fdsReleased := baselineFDs < 0 || currentFDs <= baselineFDs+tolerance
		if fdsReleased && currentGoroutines <= baselineGoroutines+tolerance {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"resource growth after %d sessions: file descriptors %d -> %d, goroutines %d -> %d",
				xhttpResourceSoakIterations,
				baselineFDs,
				currentFDs,
				baselineGoroutines,
				currentGoroutines,
			)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
