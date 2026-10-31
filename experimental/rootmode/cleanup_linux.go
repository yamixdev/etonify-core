//go:build linux

package rootmode

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/sagernet/netlink"
	"golang.org/x/sys/unix"
)

type journal struct {
	Protocol int `json:"protocol"`
	UID      int `json:"uid"`
}

func readJournal(work *os.File) (*journal, error) {
	file, err := openRelativeRegular(int(work.Fd()), "ownership.json", 0)
	if err != nil {
		var stat unix.Stat_t
		if unix.Fstatat(int(work.Fd()), "ownership.json", &stat, unix.AT_SYMLINK_NOFOLLOW) == unix.ENOENT {
			return nil, nil
		}
		return nil, Reason("journal_denied")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1024))
	decoder.DisallowUnknownFields()
	var value journal
	if decoder.Decode(&value) != nil || value.Protocol != Protocol || value.UID%100000 < 10000 {
		return nil, Reason("journal_denied")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, Reason("journal_denied")
	}
	return &value, nil
}

func writeJournal(work *os.File, uid int) error {
	fd, err := unix.Openat(int(work.Fd()), "ownership.pending", unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return Reason("journal_denied")
	}
	file := os.NewFile(uintptr(fd), "ownership.json")
	defer file.Close()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0077 != 0 || stat.Nlink != 1 || file.Truncate(0) != nil {
		return Reason("journal_denied")
	}
	if json.NewEncoder(file).Encode(journal{Protocol, uid}) != nil || file.Sync() != nil || unix.Renameat(int(work.Fd()), "ownership.pending", int(work.Fd()), "ownership.json") != nil || work.Sync() != nil {
		return Reason("journal_denied")
	}
	return nil
}

func Cleanup(options RunOptions) error {
	if err := RequireRoot(os.Geteuid()); err != nil {
		return err
	}
	if options.WorkDir != WorkDirectory {
		return Reason("work_dir_denied")
	}
	work, err := openWorkDir()
	if err != nil {
		return err
	}
	defer work.Close()
	lock, err := lockRuntime(work)
	if err != nil {
		return err
	}
	defer lock.Close()
	return recoverOwned(work)
}

func recoverOwned(work *os.File) error {
	ownership, err := readJournal(work)
	if err != nil {
		return err
	}
	if ownership == nil {
		return clearSnapshots(work)
	}
	if err = cleanupIPTables(); err != nil {
		return err
	}
	rules, err := netlink.RuleList(netlink.FAMILY_ALL)
	if err != nil {
		return Reason("cleanup_failed")
	}
	var result error
	for _, rule := range rules {
		if ownsRule(rule) {
			if err = netlink.RuleDel(&rule); err != nil && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ESRCH) {
				result = Reason("cleanup_failed")
			}
		}
	}
	for _, table := range []int{rootRouteTable, rootRedirectTable, rootTProxyTable} {
		routes, listErr := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
		if listErr != nil {
			result = Reason("cleanup_failed")
			continue
		}
		for _, route := range routes {
			if int(route.Protocol) == 239 {
				if err = netlink.RouteDel(&route); err != nil && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ESRCH) {
					result = Reason("cleanup_failed")
				}
			}
		}
	}
	if result != nil {
		return result
	}
	if unix.Unlinkat(int(work.Fd()), "ownership.json", 0) != nil || work.Sync() != nil {
		return Reason("cleanup_failed")
	}
	return clearSnapshots(work)
}

func ownsRule(rule netlink.Rule) bool {
	if rule.Src.IsValid() || rule.Dst.IsValid() || rule.IifName != "" || rule.OifName != "" || rule.UIDRange != nil || rule.Dport != nil || rule.Sport != nil || rule.Tos != 0 || rule.IPProto != 0 {
		return false
	}
	if rule.Priority == rootRedirectPriority {
		return (rule.Table == rootRedirectTable && rule.Mark == 0) || (rule.Table == rootTProxyTable && rule.Mark == rootTProxyMark && rule.Mask == rootMarkMask)
	}
	switch rule.Priority {
	case rootRulePriority:
		return rule.Mark == rootOutputMark && rule.Mask == rootMarkMask && rule.Goto == rootRulePriority+2
	case rootRulePriority + 1:
		return rule.Mark == rootInputMark && rule.Mask == rootMarkMask && rule.Table == rootRouteTable
	case rootRulePriority + 2:
		return rule.Mark == 0 && (rule.Table == -1 || rule.Table == 0 || rule.Table == unix.RT_TABLE_MAIN)
	case rootFallbackPriority:
		return rule.Mark == rootOutputMark && rule.Mask == rootMarkMask && rule.Table == rootRouteTable && rule.Invert
	}
	return false
}

func preflight() error {
	rules, err := netlink.RuleList(netlink.FAMILY_ALL)
	if err != nil {
		return Reason("resource_check_failed")
	}
	for _, rule := range rules {
		if conflictsWithRootRouting(rule.Priority, rule.Table, rule.Mark) {
			return Reason("resource_conflict")
		}
	}
	for _, table := range []int{rootRouteTable, rootRedirectTable, rootTProxyTable} {
		routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
		if err != nil {
			return Reason("resource_check_failed")
		}
		if len(routes) > 0 {
			return Reason("resource_conflict")
		}
	}
	if _, err = netlink.LinkByName("etonify0"); err == nil {
		return Reason("resource_conflict")
	}
	for _, binary := range []string{"/system/bin/iptables", "/system/bin/ip6tables"} {
		if _, err = os.Stat(binary); os.IsNotExist(err) {
			if strings.HasSuffix(binary, "ip6tables") {
				continue
			}
			return Reason("iptables_unavailable")
		}
		for _, table := range []string{"nat", "mangle", "filter"} {
			output, err := exec.Command(binary, "-w", "5", "-t", table, "-S").Output()
			if err != nil {
				return Reason("resource_check_failed")
			}
			for _, line := range strings.Split(string(output), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "-N" && OwnsChain(fields[1]) {
					return Reason("resource_conflict")
				}
			}
		}
	}
	return nil
}

func cleanupIPTables() error {
	for _, binary := range []string{"/system/bin/iptables", "/system/bin/ip6tables"} {
		if _, err := os.Stat(binary); os.IsNotExist(err) {
			if strings.HasSuffix(binary, "ip6tables") {
				continue
			}
			return Reason("iptables_unavailable")
		}
		for _, table := range []string{"nat", "mangle", "filter"} {
			output, err := exec.Command(binary, "-w", "5", "-t", table, "-S").Output()
			if err != nil {
				return Reason("cleanup_failed")
			}
			var chains []string
			var references [][]string
			for _, line := range strings.Split(string(output), "\n") {
				if strings.ContainsAny(line, "\"'\\") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				if fields[0] == "-N" && OwnsChain(fields[1]) {
					chains = append(chains, fields[1])
				}
				if fields[0] == "-A" && !OwnsChain(fields[1]) {
					for i := 2; i < len(fields)-1; i++ {
						if fields[i] == "-j" && OwnsChain(fields[i+1]) {
							references = append(references, fields[1:])
							break
						}
					}
				}
			}
			run := func(args ...string) error {
				if exec.Command(binary, append([]string{"-w", "5", "-t", table}, args...)...).Run() != nil {
					return Reason("cleanup_failed")
				}
				return nil
			}
			for _, ref := range references {
				if err = run(append([]string{"-D"}, ref...)...); err != nil {
					return err
				}
			}
			for _, chain := range chains {
				if err = run("-F", chain); err != nil {
					return err
				}
			}
			for _, chain := range chains {
				if err = run("-X", chain); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
