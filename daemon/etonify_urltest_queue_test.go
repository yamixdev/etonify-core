package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

func TestURLTestQueuePriorityDoesNotDuplicateClaimedJobs(t *testing.T) {
	q := newURLTestQueue([]urlTestTarget{{tag: "a"}, {tag: "b"}, {tag: "c"}})
	a, _ := q.take()
	if a.tag != "a" {
		t.Fatal(a.tag)
	}
	if !q.prioritize("a") {
		t.Fatal("claimed target must join the owning queue")
	}
	if q.prioritize("missing") {
		t.Fatal("unknown target must not join the queue")
	}
	q.prioritize("c")
	q.prioritize("c")
	c, _ := q.take()
	b, _ := q.take()
	if c.tag != "c" || b.tag != "b" {
		t.Fatal(c.tag, b.tag)
	}
	if _, ok := q.take(); ok {
		t.Fatal("duplicate job")
	}
}

func TestURLTestQueueRemove(t *testing.T) {
	q := newURLTestQueue([]urlTestTarget{{tag: "a"}, {tag: "b"}, {tag: "c"}})
	if !q.remove("b") {
		t.Fatal("expected b to be removed")
	}
	if q.remove("b") {
		t.Fatal("expected second remove of b to return false")
	}
	if q.remove("non-existent") {
		t.Fatal("expected remove of non-existent to return false")
	}
	first, _ := q.take()
	second, _ := q.take()
	if first.tag != "a" || second.tag != "c" {
		t.Fatalf("unexpected queue order after remove: %s, %s", first.tag, second.tag)
	}
	if _, ok := q.take(); ok {
		t.Fatal("expected empty queue")
	}
}

func TestURLTestPublishesBeforeQueueCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	results := make(chan string, 2)
	done := make(chan struct{})
	defer func() {
		close(release)
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("worker did not stop")
		}
	}()
	calls := 0
	go func() {
		defer close(done)
		runURLTestTargets(ctx, []urlTestTarget{{tag: "a"}, {tag: "b"}},
			urlTestSessionOptions{timeout: time.Second, concurrency: 1},
			func(ctx context.Context, _ string, _ adapter.Outbound) (uint16, error) {
				calls++
				if calls == 2 {
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return 50, nil
			}, func(target urlTestTarget, _ uint16, _ error) { results <- target.tag })
	}()
	select {
	case tag := <-results:
		if tag != "a" {
			t.Fatal(tag)
		}
	case <-time.After(time.Second):
		t.Fatal("first result blocked by queue")
	}
	select {
	case <-done:
		t.Fatal("queue should still be running")
	default:
	}
}

func TestURLTestServiceCleanupCancelsAndReleasesSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &StartedService{urlTestSessions: map[string]*urlTestSession{
		"auto": {ctx: ctx, cancel: cancel, full: true},
	}}
	s.cancelURLTestSessions()
	if ctx.Err() == nil || len(s.urlTestSessions) != 0 {
		t.Fatal("cleanup retained an active session")
	}
}

func TestURLTestFinishedSessionRetention(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "canceled"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session := &urlTestSession{ctx: ctx, cancel: cancel, full: true}
			s := &StartedService{urlTestSessions: map[string]*urlTestSession{"auto": session}}
			defer s.cancelURLTestSessions()
			if canceled {
				cancel()
			}
			s.finishURLTestSession("auto", session)
			if session.completed == canceled || (s.urlTestSessions["auto"] != nil) == canceled {
				t.Fatal("only completed sessions may absorb late priority requests")
			}
		})
	}
}
