package daemon

import (
	"testing"
)

func TestValidateOutboundHTTPURL(t *testing.T) {
	for _, rawURL := range []string{
		"https://example.com/subscription",
		"http://localhost/subscription",
		"http://127.0.0.1/subscription",
		"http://[::1]/subscription",
	} {
		if _, err := validateOutboundHTTPURL(rawURL); err != nil {
			t.Fatalf("expected %q to be accepted: %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{
		"http://example.com/subscription",
		"ftp://example.com/subscription",
		"https://user:password@example.com/subscription",
		"not-a-url",
	} {
		if _, err := validateOutboundHTTPURL(rawURL); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
}

func TestOutboundHTTPHeaderAllowed(t *testing.T) {
	for _, name := range []string{"Accept", "Authorization", "User-Agent", "X-Subscription-Token"} {
		if !outboundHTTPHeaderAllowed(name) {
			t.Fatalf("expected %q to be accepted", name)
		}
	}
	for _, name := range []string{"", "Connection", "Content-Length", "Host", "Proxy-Authorization", "Transfer-Encoding"} {
		if outboundHTTPHeaderAllowed(name) {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}
