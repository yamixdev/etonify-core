package group

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type selectorInterruptOutbound struct {
	adapter.Outbound
	tag  string
	held net.Conn
}

func (o *selectorInterruptOutbound) Type() string           { return C.TypeDirect }
func (o *selectorInterruptOutbound) Tag() string            { return o.tag }
func (o *selectorInterruptOutbound) Network() []string      { return []string{N.NetworkTCP, N.NetworkUDP} }
func (o *selectorInterruptOutbound) Dependencies() []string { return nil }
func (o *selectorInterruptOutbound) NewConnection(_ context.Context, conn net.Conn, _ adapter.InboundContext, _ N.CloseHandlerFunc) {
	o.held = conn
}

type selectorInterruptManager struct {
	adapter.OutboundManager
	items map[string]adapter.Outbound
}

func (m *selectorInterruptManager) Outbound(tag string) (adapter.Outbound, bool) {
	item, loaded := m.items[tag]
	return item, loaded
}

func (m *selectorInterruptManager) Outbounds() []adapter.Outbound {
	result := make([]adapter.Outbound, 0, len(m.items))
	for _, item := range m.items {
		result = append(result, item)
	}
	return result
}

func (m *selectorInterruptManager) Default() adapter.Outbound { return nil }

func TestSelectorInterruptsInboundHandlerConnection(t *testing.T) {
	first := &selectorInterruptOutbound{tag: "first"}
	second := &selectorInterruptOutbound{tag: "second"}
	manager := &selectorInterruptManager{items: map[string]adapter.Outbound{
		"first":  first,
		"second": second,
	}}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	ctx = service.ContextWith[adapter.ConnectionManager](ctx, route.NewConnectionManager(logger.NOP()))
	selector := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, "selector", nil, []string{"first", "second"}),
		ctx:                          ctx,
		outbound:                     manager,
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger.NOP(),
		tags:                         []string{"first", "second"},
		defaultTag:                   "first",
		outbounds:                    manager.items,
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: true,
	}
	if err := selector.Start(); err != nil {
		t.Fatal(err)
	}

	client, server := net.Pipe()
	defer client.Close()
	selector.NewConnection(context.Background(), server, adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddr("example.com:443"),
	}, func(error) {})
	if first.held == nil {
		t.Fatal("selected handler did not receive the inbound connection")
	}
	if !selector.SelectOutbound("second") {
		t.Fatal("failed to select the second outbound")
	}

	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("inbound connection remained open after selector switch")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("selector switch did not interrupt the inbound connection")
	}
}
