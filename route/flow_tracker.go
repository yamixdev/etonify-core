package route

import (
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
)

var (
	_ tun.FlowTracker = (*flowInterrupter)(nil)
	_ tun.FlowTracker = multiFlowTracker(nil)
)

type flowInterrupter struct {
	chain    []adapter.Outbound
	network  string
	access   sync.Mutex
	removers []func()
	closed   bool
}

func newFlowInterrupter(chain []adapter.Outbound, network string) *flowInterrupter {
	hasGroup := false
	for _, outbound := range chain {
		if _, ok := outbound.(adapter.OutboundGroup); ok {
			hasGroup = true
			break
		}
	}
	if !hasGroup {
		return nil
	}
	return &flowInterrupter{chain: chain, network: network}
}

type flowCloser struct{ tun.FlowHandle }

func (c flowCloser) Close() error {
	c.CloseFlow()
	return nil
}

func (t *flowInterrupter) AttachFlow(handle tun.FlowHandle) {
	var removers []func()
	for _, outbound := range t.chain {
		if group, ok := outbound.(adapter.OutboundGroup); ok {
			removers = append(removers, group.AttachConnection(flowCloser{handle}))
		}
	}
	t.access.Lock()
	if t.closed {
		t.access.Unlock()
		for _, remove := range removers {
			remove()
		}
		return
	}
	t.removers = removers
	t.access.Unlock()
	if !outboundChainSelected(t.chain, t.network) {
		t.CloseFlow(tun.FlowCloseFinished)
		handle.CloseFlow()
	}
}

func (t *flowInterrupter) CountForward(int) {}
func (t *flowInterrupter) CountReverse(int) {}
func (t *flowInterrupter) FlowEstablished() {}

func (t *flowInterrupter) CloseFlow(tun.FlowCloseReason) {
	t.access.Lock()
	if t.closed {
		t.access.Unlock()
		return
	}
	t.closed = true
	removers := t.removers
	t.removers = nil
	t.access.Unlock()
	for _, remove := range removers {
		remove()
	}
}

type multiFlowTracker []tun.FlowTracker

func (t multiFlowTracker) AttachFlow(handle tun.FlowHandle) {
	for _, tracker := range t {
		tracker.AttachFlow(handle)
	}
}

func (t multiFlowTracker) CountForward(n int) {
	for _, tracker := range t {
		tracker.CountForward(n)
	}
}

func (t multiFlowTracker) CountReverse(n int) {
	for _, tracker := range t {
		tracker.CountReverse(n)
	}
}

func (t multiFlowTracker) FlowEstablished() {
	for _, tracker := range t {
		tracker.FlowEstablished()
	}
}

func (t multiFlowTracker) CloseFlow(reason tun.FlowCloseReason) {
	for _, tracker := range t {
		tracker.CloseFlow(reason)
	}
}
