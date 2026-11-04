package urltest

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEligibilityRejectsLateResultEvenAfterReset(t *testing.T) {
	h := NewHistoryStorage()
	old, ctx := h.Eligibility()
	h.SetExcludedTags([]string{"a"})
	require.Error(t, ctx.Err())
	revision, _ := h.Eligibility()
	require.False(t, h.StoreForEligibility(h.Generation(), revision, "a", &adapter.URLTestHistory{Delay: 1}))
	h.SetExcludedTags(nil)
	require.False(t, h.StoreForEligibility(h.Generation(), old, "a", &adapter.URLTestHistory{Delay: 1}))
	revision, _ = h.Eligibility()
	require.True(t, h.StoreForEligibility(h.Generation(), revision, "a", &adapter.URLTestHistory{Delay: 2}))
	require.EqualValues(t, 2, h.LoadCurrentURLTestHistory("a").Delay)
}
