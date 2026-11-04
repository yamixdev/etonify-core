package group

import (
	"github.com/sagernet/sing-box/adapter"
)

func (g *URLTestGroup) eligible(outbound adapter.Outbound, network string) bool {
	if outbound == nil {
		return false
	}
	tag := RealTag(outbound, network)
	return tag != "" && !g.history.IsExcluded(tag)
}

// RefreshEligibility affects future dials only. No attached connections close.
func (g *URLTestGroup) RefreshEligibility() { g.performUpdateCheckWithInterrupt(false) }
func (s *URLTest) RefreshURLTestEligibility() {
	if s.group != nil {
		s.group.RefreshEligibility()
	}
}
