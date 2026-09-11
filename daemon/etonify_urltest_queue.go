package daemon

import "sync"

// Only queued jobs can move. Claimed (including completed) jobs never re-enter
// the queue, so a priority request cannot create a second probe of a leaf.
type urlTestQueue struct {
	access  sync.Mutex
	pending []urlTestTarget
}

func newURLTestQueue(targets []urlTestTarget) *urlTestQueue {
	return &urlTestQueue{pending: append([]urlTestTarget(nil), targets...)}
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

func (q *urlTestQueue) prioritize(tag string) {
	q.access.Lock()
	defer q.access.Unlock()
	for i, target := range q.pending {
		if target.tag == tag {
			copy(q.pending[1:i+1], q.pending[:i])
			q.pending[0] = target
			return
		}
	}
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
