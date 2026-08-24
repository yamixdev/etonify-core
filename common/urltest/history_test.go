package urltest

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
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
