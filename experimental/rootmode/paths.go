package rootmode

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

func AppFiles(uid int) string {
	return fmt.Sprintf("/data/user/%d/com.etonify.meow_client/files", uid/100000)
}

func RuleRelativePath(value string, uid int) (string, error) {
	if uid%100000 < 10000 || path.Clean(value) != value || strings.ContainsAny(value, "\x00\\") {
		return "", Reason("rule_file_denied")
	}
	relative, ok := strings.CutPrefix(value, AppFiles(uid)+"/")
	if !ok {
		return "", Reason("rule_file_denied")
	}
	for _, prefix := range []string{"root-rule-staging/", "route-data/", "adblock/"} {
		if strings.HasPrefix(relative, prefix) && len(relative) > len(prefix) {
			return relative, nil
		}
	}
	return "", Reason("rule_file_denied")
}

func OwnsChain(name string) bool {
	suffix, ok := strings.CutPrefix(name, "etonify-")
	if !ok {
		return false
	}
	switch suffix {
	case "forward", "input", "output", "output-loopback", "output-mark", "output-prematch", "output-protect", "output-tproxy", "postrouting", "prerouting", "prerouting-local", "prerouting-loopback", "prerouting-mark", "prerouting-prematch", "prerouting-tproxy", "reset", "unreachable-forward", "unreachable-output":
		return true
	}
	prefix, counter, ok := strings.Cut(suffix, "-")
	if !ok {
		return false
	}
	switch prefix {
	case "cont", "include", "local", "redirect":
		_, err := strconv.ParseUint(counter, 10, 32)
		return err == nil
	}
	return false
}
