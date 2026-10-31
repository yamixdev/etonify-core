//go:build linux

package rootmode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/netlink"
	"golang.org/x/sys/unix"
)

func TestPrivateRuleDescriptorRejectsSymlinksAndHardlinks(t *testing.T) {
	directory := t.TempDir()
	base, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	file := filepath.Join(directory, "valid.srs")
	if err = os.WriteFile(file, []byte("rules"), 0600); err != nil {
		t.Fatal(err)
	}
	opened, err := openRelativeRegular(int(base.Fd()), "valid.srs", os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
	if err = os.Symlink(file, filepath.Join(directory, "symlink.srs")); err != nil {
		t.Fatal(err)
	}
	if opened, err = openRelativeRegular(int(base.Fd()), "symlink.srs", os.Getuid()); err == nil {
		opened.Close()
		t.Fatal("symlink accepted")
	}
	if err = os.Link(file, filepath.Join(directory, "hardlink.srs")); err != nil {
		t.Fatal(err)
	}
	if opened, err = openRelativeRegular(int(base.Fd()), "valid.srs", os.Getuid()); err == nil {
		opened.Close()
		t.Fatal("hardlink accepted")
	}
}

func TestCrashCleanupDoesNotOwnForeignRulesAtSamePriority(t *testing.T) {
	own := *netlink.NewRule()
	own.Priority = rootRulePriority + 1
	own.Mark = 0x4000000
	own.Mask = 0x1C000000
	own.Table = 20239
	own.Family = unix.AF_INET
	if !ownsRule(own) {
		t.Fatal("own rule not recognized")
	}
	foreign := own
	foreign.Table = 2022
	if ownsRule(foreign) {
		t.Fatal("other-client table accepted")
	}
	foreign = own
	foreign.Mark = 0x400000
	if ownsRule(foreign) {
		t.Fatal("sing-box mark accepted")
	}
	foreign = own
	foreign.IifName = "other0"
	if ownsRule(foreign) {
		t.Fatal("foreign selector accepted")
	}
	terminal := *netlink.NewRule()
	terminal.Priority = rootRulePriority + 2
	if !ownsRule(terminal) {
		t.Fatal("own NOP rule lost")
	}
}

func TestCrashCleanupPreservesAndroidStockRules(t *testing.T) {
	for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
		for _, entry := range []struct{ priority, table int }{
			{0, 255}, {15000, 1005}, {16000, 1005}, {17000, 1005},
			{18000, 99}, {19000, 98}, {20000, 97}, {31000, 1005},
		} {
			rule := *netlink.NewRule()
			rule.Family = family
			rule.Priority, rule.Table = entry.priority, entry.table
			if ownsRule(rule) {
				t.Errorf("cleanup owns Android rule %+v", rule)
			}
			if conflictsWithRootRouting(rule.Priority, rule.Table, rule.Mark) {
				t.Errorf("Android rule blocks preflight: %+v", rule)
			}
		}
	}
}

func TestCrashCleanupOwnsNewRoutingRules(t *testing.T) {
	for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
		for _, entry := range []struct {
			priority, table, gotoTarget int
			mark                        uint32
			mask                        int
			invert                      bool
		}{
			{14899, 20240, -1, 0, -1, false},
			{14899, 20241, -1, 0x10000000, 0x1C000000, false},
			{14900, -1, 14902, 0x8000000, 0x1C000000, false},
			{14901, 20239, -1, 0x4000000, 0x1C000000, false},
			{14902, -1, -1, 0, -1, false},
			{14920, 20239, -1, 0x8000000, 0x1C000000, true},
		} {
			rule := *netlink.NewRule()
			rule.Family = family
			rule.Priority, rule.Table, rule.Goto = entry.priority, entry.table, entry.gotoTarget
			rule.Mark, rule.Mask, rule.Invert = entry.mark, entry.mask, entry.invert
			if !ownsRule(rule) {
				t.Errorf("new owned rule is not recovered: %+v", rule)
			}
			foreign := rule
			foreign.Table = 1005
			foreign.Goto = -1
			if ownsRule(foreign) {
				t.Errorf("cleanup owns foreign rule: %+v", foreign)
			}
		}
	}
}

func TestSocketCleanupPreservesReplacement(t *testing.T) {
	directory := t.TempDir()
	dir, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(directory, "command.sock")
	if err = os.WriteFile(original, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	node, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(original, filepath.Join(directory, "old.sock")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(original, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	endpoint := &socketEndpoint{dir: dir, node: node}
	if err = endpoint.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "replacement" {
		t.Fatal("replacement socket path removed")
	}
}
