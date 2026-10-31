package rootmode

const (
	rootRouteTable    = 20239
	rootRedirectTable = 20240
	rootTProxyTable   = 20241
	// Keep Android's secure VPN/lockdown rules (through 14000), but run
	// before UID explicit network (15000) and explicit network (16000).
	rootRedirectPriority = 14899
	rootRulePriority     = 14900
	rootFallbackPriority = 14920
	rootInputMark        = 0x4000000
	rootOutputMark       = 0x8000000
	rootResetMark        = 0xC000000
	rootTProxyMark       = 0x10000000
	rootMarkMask         = rootInputMark | rootOutputMark | rootResetMark | rootTProxyMark
)

func conflictsWithRootRouting(priority, table int, mark uint32) bool {
	return priority >= rootRedirectPriority && priority <= rootFallbackPriority ||
		table == rootRouteTable || table == rootRedirectTable || table == rootTProxyTable ||
		mark&rootMarkMask != 0
}
