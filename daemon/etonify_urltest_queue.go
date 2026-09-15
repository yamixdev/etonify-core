package daemon

import "sync"

// Only queued jobs can move. Claimed (including completed) jobs never re-enter
// the queue, so a priority request cannot create a second probe of a leaf.
type urlTestQueue struct {
	access  sync.Mutex
	pending []urlTestTarget
	known   map[string]struct{}
}

func newURLTestQueue(targets []urlTestTarget) *urlTestQueue {
	known := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		known[target.tag] = struct{}{}
	}
	return &urlTestQueue{
		pending: append([]urlTestTarget(nil), targets...),
		known:   known,
	}
}

func (q *urlTestQueue) take() (urlTestTarget, bool) {
	q.access.Lock()
	defer q.access.Unlock()
	if len(q.pending) == 0 {
		return urlTestTarget{}, false
	}
	target := q.pending[0]
	q.pending[0] = urlTestTarget{}
	q.pending = q.pending[1:]
	return target, true
}

// prioritize moves a queued target to the front. It also returns true for a
// target that has already been claimed or completed, allowing a targeted UI
// request to join the owning full session instead of starting a duplicate
// network probe.
func (q *urlTestQueue) prioritize(tag string) bool {
	q.access.Lock()
	defer q.access.Unlock()
	if _, exists := q.known[tag]; !exists {
		return false
	}
	for i, target := range q.pending {
		if target.tag == tag {
			copy(q.pending[1:i+1], q.pending[:i])
			q.pending[0] = target
			return true
		}
	}
	return true
}

func (q *urlTestQueue) remove(tag string) bool {
	q.access.Lock()
	defer q.access.Unlock()
	for i, target := range q.pending {
		if target.tag == tag {
			copy(q.pending[i:], q.pending[i+1:])
			q.pending[len(q.pending)-1] = urlTestTarget{}
			q.pending = q.pending[:len(q.pending)-1]
			return true
		}
	}
	return false
}
