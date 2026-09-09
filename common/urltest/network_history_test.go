package urltest

import (
	"github.com/sagernet/sing-box/adapter"
	"testing"
	"time"
)

func TestNetworkHistoryRejectsOldProbe(t *testing.T) {
	history := NewHistoryStorage()
	old := history.Generation()
	entry := &adapter.URLTestHistory{Time: time.Now(), Delay: 45}
	history.StoreForGeneration(old, "a", entry)
	history.StoreForGeneration(old, "b", entry)
	history.ResetNetwork()
	if history.LoadURLTestHistory("a") != nil || history.LoadURLTestHistory("b") != nil {
		t.Fatal("old network history survived")
	}
	if history.StoreForGeneration(old, "a", entry) {
		t.Fatal("late result from old network was accepted")
	}
	if !history.StoreForGeneration(history.Generation(), "a", entry) {
		t.Fatal("fresh network result rejected")
	}
}
