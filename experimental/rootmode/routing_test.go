package rootmode

import "testing"

func TestStockAndroidRoutingDoesNotConflictWithRoot(t *testing.T) {
	// Representative IPv4/IPv6 RPDB entries from Android 15 netd's
	// RouteController: UID explicit, explicit, oif, legacy and defaults.
	for _, rule := range []struct {
		priority, table int
		mark            uint32
	}{
		{0, 255, 0},
		{15000, 1005, 0x10065},
		{16000, 1005, 0x10065},
		{17000, 1005, 0},
		{18000, 99, 0},
		{19000, 98, 0},
		{20000, 97, 0},
		{23000, 1005, 0x65},
		{31000, 1005, 0},
		{32000, 0, 0},
	} {
		if conflictsWithRootRouting(rule.priority, rule.table, rule.mark) {
			t.Errorf("stock Android rule %+v blocks root startup", rule)
		}
	}
}

func TestRootRoutingRejectsForeignReservedResources(t *testing.T) {
	for _, rule := range []struct {
		priority, table int
		mark            uint32
	}{
		{14899, 1005, 0},
		{14900, 1005, 0},
		{14902, 1005, 0},
		{14920, 1005, 0},
		{100, 20239, 0},
		{100, 20240, 0},
		{100, 20241, 0},
		{100, 1005, 0x4000000},
		{100, 1005, 0x8000000},
		{100, 1005, 0x10000000},
	} {
		if !conflictsWithRootRouting(rule.priority, rule.table, rule.mark) {
			t.Errorf("foreign rule %+v can collide with root routing", rule)
		}
	}
}
