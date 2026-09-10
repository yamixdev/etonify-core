package urltest

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

func TestNetworkHistoryRetainsFallbackAndRejectsOldProbe(t *testing.T) {
	history := NewHistoryStorage()
	old := history.Generation()
	entry := &adapter.URLTestHistory{Time: time.Now(), Delay: 45}
	history.StoreForGeneration(old, "a", entry)
	history.StoreForGeneration(old, "b", entry)
	history.ResetNetwork()

	require.Nil(t, history.LoadURLTestHistory("a"))
	require.Equal(t, uint16(45), history.LoadFallbackURLTestHistory("a").Delay)
	require.Nil(t, history.LoadCurrentURLTestHistory("a"))
	require.False(t, history.StoreForGeneration(old, "a", entry))

	freshGeneration := history.Generation()
	fresh := &adapter.URLTestHistory{Time: time.Now(), Delay: 81}
	require.True(t, history.StoreForGeneration(freshGeneration, "a", fresh))
	require.Equal(t, uint16(81), history.LoadCurrentURLTestHistory("a").Delay)
	require.Nil(t, history.LoadCurrentURLTestHistory("b"))
}
