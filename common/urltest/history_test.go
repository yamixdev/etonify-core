package urltest

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/observable"
	"github.com/stretchr/testify/require"
)

func TestHistoryStorageCopiesValues(t *testing.T) {
	storage := NewHistoryStorage()
	history := &adapter.URLTestHistory{Time: time.Now(), Delay: 42, Status: adapter.URLTestStatusAvailable}
	storage.StoreURLTestHistory("proxy", history)
	history.Delay = 99

	loaded := storage.LoadURLTestHistory("proxy")
	require.NotNil(t, loaded)
	require.Equal(t, uint16(42), loaded.Delay)
	loaded.Delay = 7
	require.Equal(t, uint16(42), storage.LoadURLTestHistory("proxy").Delay)
}

func TestHistoryStorageTreatsNilAsDelete(t *testing.T) {
	storage := NewHistoryStorage()
	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{Delay: 42})
	storage.StoreURLTestHistory("proxy", nil)
	require.Nil(t, storage.LoadURLTestHistory("proxy"))
}

func TestSelectionUpdateHookIgnoresIndividualResults(t *testing.T) {
	storage := NewHistoryStorage()
	updates := observable.NewSubscriber[struct{}](4)
	selectionUpdates := observable.NewSubscriber[struct{}](4)
	storage.AddUpdateHook(updates)
	storage.AddSelectionUpdateHook(selectionUpdates)
	updateSubscription, _ := updates.Subscription()
	selectionSubscription, _ := selectionUpdates.Subscription()

	storage.StoreURLTestHistory("proxy", &adapter.URLTestHistory{Delay: 42})
	requireReceivesUpdate(t, updateSubscription)
	requireNoUpdate(t, selectionSubscription)

	storage.NotifyUpdated()
	requireReceivesUpdate(t, updateSubscription)
	requireReceivesUpdate(t, selectionSubscription)
}

func TestHistoryStorageExternalManagerFlag(t *testing.T) {
	storage := NewHistoryStorage()
	require.False(t, storage.ExternallyManaged())
	storage.SetExternallyManaged(true)
	require.True(t, storage.ExternallyManaged())
}

func requireReceivesUpdate(t *testing.T, subscription <-chan struct{}) {
	t.Helper()
	select {
	case <-subscription:
	case <-time.After(time.Second):
		t.Fatal("expected update notification")
	}
}

func requireNoUpdate(t *testing.T, subscription <-chan struct{}) {
	t.Helper()
	select {
	case <-subscription:
		t.Fatal("unexpected update notification")
	default:
	}
}
