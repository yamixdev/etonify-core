package daemon

import (
	"sync"
	"time"
)

// Coalesces selection updates while a large URLTest queue is still running.
// The first result is immediate; later results get a bounded trailing update.
type urlTestSelectionUpdater struct {
	access        sync.Mutex
	refreshAccess sync.Mutex
	interval      time.Duration
	refresh       func()
	lastRefresh   time.Time
	timer         *time.Timer
	closed        bool
}

func newURLTestSelectionUpdater(interval time.Duration, refresh func()) *urlTestSelectionUpdater {
	return &urlTestSelectionUpdater{interval: interval, refresh: refresh}
}

func (u *urlTestSelectionUpdater) onResult() {
	u.access.Lock()
	if u.closed {
		u.access.Unlock()
		return
	}
	now := time.Now()
	if u.lastRefresh.IsZero() || !now.Before(u.lastRefresh.Add(u.interval)) {
		u.lastRefresh = now
		u.access.Unlock()
		u.refreshIfOpen()
		return
	}
	if u.timer == nil {
		u.timer = time.AfterFunc(time.Until(u.lastRefresh.Add(u.interval)), u.onTimer)
	}
	u.access.Unlock()
}

func (u *urlTestSelectionUpdater) onTimer() {
	u.access.Lock()
	if u.closed {
		u.access.Unlock()
		return
	}
	u.timer = nil
	u.lastRefresh = time.Now()
	u.access.Unlock()
	u.refreshIfOpen()
}

func (u *urlTestSelectionUpdater) refreshIfOpen() {
	u.refreshAccess.Lock()
	defer u.refreshAccess.Unlock()
	u.access.Lock()
	open := !u.closed
	u.access.Unlock()
	if open {
		u.refresh()
	}
}

func (u *urlTestSelectionUpdater) finish() {
	u.access.Lock()
	if u.closed {
		u.access.Unlock()
		return
	}
	u.closed = true
	if u.timer != nil {
		u.timer.Stop()
		u.timer = nil
	}
	u.access.Unlock()
	u.refreshAccess.Lock()
	u.refresh()
	u.refreshAccess.Unlock()
}
