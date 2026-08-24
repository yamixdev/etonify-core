//go:build with_utls

package tls

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRealityClientFallbackURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://example.com", realityClientFallbackURL("example.com", ""))
	require.Equal(t, "https://example.com/path", realityClientFallbackURL("example.com", "/path"))
	require.Equal(t, "https://example.com?ed=2560", realityClientFallbackURL("example.com", "?ed=2560"))
	require.Equal(t, "https://example.com/path", realityClientFallbackURL("example.com", "path"))
}
