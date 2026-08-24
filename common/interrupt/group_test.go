package interrupt

import (
	"net"
	"sync"
	"testing"
	"time"
)

type closeBarrierConn struct {
	net.Conn
	barrier *sync.WaitGroup
}

type reentrantCloseConn struct {
	net.Conn
	group *Group
}

func (c *reentrantCloseConn) Close() error {
	probe, peer := net.Pipe()
	_ = c.group.NewConn(probe, true).Close()
	_ = peer.Close()
	return c.Conn.Close()
}

func (c *closeBarrierConn) Close() error {
	c.barrier.Done()
	c.barrier.Wait()
	return c.Conn.Close()
}

func TestNestedGroupsInterruptWithoutDeadlock(t *testing.T) {
	groupA := NewGroup()
	groupB := NewGroup()
	barrier := &sync.WaitGroup{}
	barrier.Add(2)

	barrierA, barrierAPeer := net.Pipe()
	barrierB, barrierBPeer := net.Pipe()
	t.Cleanup(func() {
		_ = barrierAPeer.Close()
		_ = barrierBPeer.Close()
	})
	groupA.NewConn(&closeBarrierConn{Conn: barrierA, barrier: barrier}, true)
	groupB.NewConn(&closeBarrierConn{Conn: barrierB, barrier: barrier}, true)

	connA, connAPeer := net.Pipe()
	connB, connBPeer := net.Pipe()
	t.Cleanup(func() {
		_ = connAPeer.Close()
		_ = connBPeer.Close()
	})
	wrapperA := groupA.NewConn(connA, true)
	wrapperB := groupB.NewConn(connB, true)
	groupA.NewConn(wrapperB, true)
	groupB.NewConn(wrapperA, true)

	done := make(chan struct{}, 2)
	go func() {
		groupA.Interrupt(true)
		done <- struct{}{}
	}()
	go func() {
		groupB.Interrupt(true)
		done <- struct{}{}
	}()

	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for range 2 {
		select {
		case <-done:
		case <-timeout.C:
			t.Fatal("nested group interrupt deadlocked")
		}
	}
}

func TestWrappedConnectionCloseDoesNotHoldGroupLock(t *testing.T) {
	group := NewGroup()
	connection, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	wrapped := group.NewConn(&reentrantCloseConn{Conn: connection, group: group}, true)

	done := make(chan error, 1)
	go func() { done <- wrapped.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("wrapped connection close deadlocked while re-entering the group")
	}
}
