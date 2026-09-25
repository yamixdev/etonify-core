package v2rayhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/net/http2"
)

func TestResetIdleHTTP2Transport(t *testing.T) {
	transport := &http2.Transport{}
	if got := ResetTransport(transport); got != transport {
		t.Fatalf("reset returned %T, want the original HTTP/2 transport", got)
	}
}

func TestResetActiveHTTP2Transport(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport := &http2.Transport{TLSClientConfig: tlsConfig}
	response, err := transport.RoundTrip(&http.Request{
		Method: http.MethodGet,
		URL:    mustParseURL(t, server.URL),
		Header: make(http.Header),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if got := ResetTransport(transport); got != transport {
		t.Fatalf("reset returned %T, want the original HTTP/2 transport", got)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
