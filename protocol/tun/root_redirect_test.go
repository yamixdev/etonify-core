package tun

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/service"
	"testing"
)

func TestRedirectOwnershipIsExplicitContextOnly(t *testing.T) {
	if name, owner := redirectOwnership(context.Background()); name != "sing-box" || owner != nil {
		t.Fatal("normal runtime ownership changed")
	}
	ctx := service.ContextWith(context.Background(), &adapter.RootRedirectOwnership{TableName: "etonify", RedirectTable: 20240, TProxyTable: 20241, RulePriority: 17999})
	name, owner := redirectOwnership(ctx)
	if name != "etonify" || owner == nil || owner.RedirectTable != 20240 || owner.TProxyTable != 20241 || owner.RulePriority != 17999 {
		t.Fatal("privileged namespace ignored")
	}
}
