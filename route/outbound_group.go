package route

import (
	"io"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

func resolveOutbound(outbound adapter.Outbound, network string) ([]adapter.Outbound, error) {
	if outbound == nil {
		return nil, E.New("missing outbound")
	}
	chain := []adapter.Outbound{outbound}
	visited := map[adapter.Outbound]bool{outbound: true}
	for {
		group, isGroup := outbound.(adapter.OutboundGroup)
		if !isGroup {
			break
		}
		selected := group.Selected(network)
		if selected == nil {
			return nil, E.New(strings.ToUpper(network), " is not supported by outbound: ", group.Tag())
		}
		if visited[selected] {
			return nil, E.New("outbound group cycle at: ", selected.Tag())
		}
		visited[selected] = true
		chain = append(chain, selected)
		outbound = selected
	}
	if !common.Contains(outbound.Network(), network) {
		return nil, E.New(strings.ToUpper(network), " is not supported by outbound: ", outbound.Tag())
	}
	return chain, nil
}

func registerInterrupt(chain []adapter.Outbound, network string, closer io.Closer, onClose N.CloseHandlerFunc) (N.CloseHandlerFunc, error) {
	var removers []func()
	for _, outbound := range chain {
		group, isGroup := outbound.(adapter.OutboundGroup)
		if isGroup {
			removers = append(removers, group.AttachConnection(closer))
		}
	}
	if !outboundChainSelected(chain, network) {
		for _, remove := range removers {
			remove()
		}
		return nil, E.New("outbound selection changed before dispatch")
	}
	if len(removers) == 0 {
		return onClose, nil
	}
	return N.AppendClose(onClose, func(error) {
		for _, remove := range removers {
			remove()
		}
	}), nil
}

func outboundChainSelected(chain []adapter.Outbound, network string) bool {
	for i, outbound := range chain[:len(chain)-1] {
		group, isGroup := outbound.(adapter.OutboundGroup)
		if isGroup && group.Selected(network) != chain[i+1] {
			return false
		}
	}
	return true
}

func reportOutboundDialFailure(chain []adapter.Outbound, network string) {
	if len(chain) == 0 {
		return
	}
	leaf := chain[len(chain)-1]
	for _, outbound := range chain[:len(chain)-1] {
		if observer, ok := outbound.(interface {
			SelectedOutboundFailed(network string, selected adapter.Outbound)
		}); ok {
			observer.SelectedOutboundFailed(network, leaf)
		}
	}
}
