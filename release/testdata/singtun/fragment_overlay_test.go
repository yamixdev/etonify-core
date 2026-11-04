package tun

import (
	"net/netip"
	"testing"
	"time"
)

func etonifyFragmentKey(id uint32) fragmentKey {
	return fragmentKey{source: netip.MustParseAddr("198.51.100.1"), destination: netip.MustParseAddr("192.0.2.1"), protocol: 17, id: id}
}

func TestEtonifyForwardFragmentExpiry(t *testing.T) {
	table := fragmentTable{entries: make(map[fragmentKey]*fragmentEntry)}
	key := etonifyFragmentKey(1)
	flow := &forwardFlow{}
	table.insert(key, flow, &rewriteRule{}, 16, 0)
	if table.lookup(key, fragmentInfo{offset: 16, more: true, payload: make([]byte, 8)}, int64(30*time.Second)) == nil {
		t.Fatal("association expired before its TTL")
	}
	if table.lookup(key, fragmentInfo{offset: 16, more: true, payload: make([]byte, 8)}, int64(30*time.Second)+1) != nil {
		t.Fatal("expired association was reused")
	}
	if len(table.entries) != 0 {
		t.Fatal("expired association retained")
	}
}

func TestEtonifyForwardFragmentCapacity(t *testing.T) {
	table := fragmentTable{entries: make(map[fragmentKey]*fragmentEntry)}
	for id := uint32(0); id < 4096; id++ {
		table.insert(etonifyFragmentKey(id), &forwardFlow{}, &rewriteRule{}, 16, 0)
	}
	table.insert(etonifyFragmentKey(4096), &forwardFlow{}, &rewriteRule{}, 16, int64(time.Second))
	if len(table.entries) != 4096 || table.entries[etonifyFragmentKey(4096)] != nil {
		t.Fatal("live fragment capacity exceeded")
	}
	// Sweep is rate-limited: less than one second since the last attempt.
	table.lastSweep = int64(30 * time.Second)
	table.insert(etonifyFragmentKey(4097), &forwardFlow{}, &rewriteRule{}, 16, int64(30*time.Second)+1)
	if table.entries[etonifyFragmentKey(4097)] != nil {
		t.Fatal("sweep interval was not respected")
	}
	table.insert(etonifyFragmentKey(4097), &forwardFlow{}, &rewriteRule{}, 16, int64(31*time.Second))
	if len(table.entries) != 1 || table.entries[etonifyFragmentKey(4097)] == nil {
		t.Fatal("expired entries prevent a new association")
	}
}

func TestEtonifyForwardFragmentOrderAndDuplicates(t *testing.T) {
	table := fragmentTable{entries: make(map[fragmentKey]*fragmentEntry)}
	key := etonifyFragmentKey(1)
	flow := &forwardFlow{}
	rule := &rewriteRule{}
	table.insert(key, flow, rule, 16, 0)
	// Last fragment before middle fragment; completing the association releases it.
	for _, info := range []fragmentInfo{{offset: 32, payload: make([]byte, 8)}, {offset: 16, more: true, payload: make([]byte, 16)}} {
		entry := table.lookup(key, info, 1)
		if entry == nil || entry.flow != flow || entry.rule != rule {
			t.Fatal("reordered tail lost its own association")
		}
	}
	if table.lookup(key, fragmentInfo{offset: 16, more: true, payload: make([]byte, 16)}, 2) != nil {
		t.Fatal("duplicate revived a completed association")
	}
	// Duplicate nonfinal fragments must not create or rewrite a different key.
	table.insert(key, flow, rule, 16, 0)
	for range 3 {
		entry := table.lookup(key, fragmentInfo{offset: 16, more: true, payload: make([]byte, 8)}, 1)
		if entry == nil || entry.flow != flow {
			t.Fatal("duplicate crossed associations")
		}
	}
	variants := []fragmentKey{etonifyFragmentKey(2), key, key, key}
	variants[1].source = netip.MustParseAddr("198.51.100.2")
	variants[2].destination = netip.MustParseAddr("192.0.2.2")
	variants[3].protocol = 6
	for _, other := range variants {
		if table.lookup(other, fragmentInfo{more: true}, 1) != nil {
			t.Fatal("fragment tuple isolation lost")
		}
	}
	if table.lookup(key, fragmentInfo{more: true}, int64(31*time.Second)) != nil {
		t.Fatal("duplicates prolonged TTL indefinitely")
	}
}

func TestEtonifyForwardFragmentDuplicateDoesNotDiscardUniqueTail(t *testing.T) {
	table := fragmentTable{entries: make(map[fragmentKey]*fragmentEntry)}
	key := etonifyFragmentKey(42)
	flow := &forwardFlow{}
	table.insert(key, flow, &rewriteRule{}, 16, 0)
	// Four distinct 16-byte regions. A duplicate at offset 16 does not cover
	// the missing region at offset 32, even when the final fragment arrived.
	for _, info := range []fragmentInfo{
		{offset: 16, more: true, payload: make([]byte, 16)},
		{offset: 16, more: true, payload: make([]byte, 16)},
		{offset: 48, payload: make([]byte, 16)},
		{offset: 32, more: true, payload: make([]byte, 16)},
	} {
		entry := table.lookup(key, info, 1)
		if entry == nil || entry.flow != flow {
			t.Fatalf("unique fragment offset=%d lost its association after a duplicate", info.offset)
		}
	}
	if table.lookup(key, fragmentInfo{offset: 32, more: true, payload: make([]byte, 16)}, 2) != nil {
		t.Fatal("completed association retained")
	}
}

type etonifySharedPort struct{}

func (*etonifySharedPort) PortAddresses() (netip.Addr, netip.Addr) {
	return netip.MustParseAddr("192.0.2.1"), netip.Addr{}
}
func (*etonifySharedPort) PortMTU() uint32             { return 1500 }
func (*etonifySharedPort) AttachReturn(Return) error   { return nil }
func (*etonifySharedPort) DetachReturn(Return) error   { return nil }
func (*etonifySharedPort) WritePackets([][]byte) error { return nil }

func TestEtonifyForwardFragmentDispatcherDoubleCloseKeepsSharedNAT(t *testing.T) {
	port := &etonifySharedPort{}
	first := NewForwardDispatcher(nil, nil, nil, 1500, UDPNatOptions{}, 0)
	second := NewForwardDispatcher(nil, nil, nil, 1500, UDPNatOptions{}, 0)
	shared := first.natFor(port)
	if second.natFor(port) != shared {
		t.Fatal("initial allocator is not shared")
	}
	defer second.Close()
	first.Close()
	first.Close()
	third := NewForwardDispatcher(nil, nil, nil, 1500, UDPNatOptions{}, 0)
	defer third.Close()
	if third.natFor(port) != shared {
		t.Fatal("repeated Close removed a NAT allocator still owned by another dispatcher")
	}
}
