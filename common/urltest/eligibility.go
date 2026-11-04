package urltest

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
)

// SetExcludedTags replaces only runtime probe/automatic-selection eligibility.
// Historical latency and manually selected groups are intentionally unchanged.
func (s *HistoryStorage) SetExcludedTags(tags []string) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.eligibilityCancel != nil {
		s.eligibilityCancel()
	}
	s.eligibilityContext, s.eligibilityCancel = context.WithCancel(context.Background())
	s.eligibilityRevision++
	s.excludedTags = make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		s.excludedTags[tag] = struct{}{}
	}
}
func (s *HistoryStorage) excludedLocked(tag string) bool { _, ok := s.excludedTags[tag]; return ok }
func (s *HistoryStorage) IsExcluded(tag string) bool {
	if s == nil {
		return false
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return s.excludedLocked(tag)
}
func (s *HistoryStorage) Eligibility() (uint64, context.Context) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.eligibilityContext == nil {
		s.eligibilityContext, s.eligibilityCancel = context.WithCancel(context.Background())
	}
	return s.eligibilityRevision, s.eligibilityContext
}
func (s *HistoryStorage) StoreForEligibility(generation, revision uint64, tag string, history *adapter.URLTestHistory) bool {
	if s == nil || history == nil {
		return false
	}
	s.access.Lock()
	if generation != s.generation || revision != s.eligibilityRevision || s.excludedLocked(tag) {
		s.access.Unlock()
		return false
	}
	s.delayHistory[tag] = networkHistoryEntry{generation: generation, history: *history}
	hooks := append(s.updateHooks[:0:0], s.updateHooks...)
	s.access.Unlock()
	notifyUpdated(hooks)
	return true
}
