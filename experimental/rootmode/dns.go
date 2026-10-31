package rootmode

import (
	"encoding/json"
	"net/netip"
	"strings"
)

func validateRuntimeDNS(data []byte) error {
	var cfg map[string]any
	if json.Unmarshal(data, &cfg) != nil {
		return Reason("config_invalid")
	}
	return checkDNS(cfg, true)
}

func validateDNS(cfg map[string]any) error {
	return checkDNS(cfg, false)
}

func checkDNS(cfg map[string]any, requireDefault bool) error {
	dns, _ := cfg["dns"].(map[string]any)
	servers, _ := dns["servers"].([]any)
	byTag := make(map[string]map[string]any, len(servers))
	for _, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			return Reason("dns_bootstrap_unsupported")
		}
		tag, _ := server["tag"].(string)
		if _, exists := byTag[tag]; exists {
			return Reason("dns_bootstrap_unsupported")
		}
		byTag[tag] = server
	}
	resolverTag := func(value any) string {
		switch value := value.(type) {
		case string:
			return value
		case map[string]any:
			tag, _ := value["server"].(string)
			return tag
		}
		return ""
	}
	var bootstrap func(string, map[string]bool) bool
	bootstrap = func(tag string, visiting map[string]bool) bool {
		if tag == "" || visiting[tag] {
			return false
		}
		server := byTag[tag]
		if server == nil || server["type"] == "fakeip" {
			return false
		}
		visiting[tag] = true
		defer delete(visiting, tag)
		if detour, _ := server["detour"].(string); detour != "" && detour != "direct" {
			return false
		}
		host, _ := server["server"].(string)
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.IsGlobalUnicast() && !addr.IsLoopback()
		}
		return bootstrap(resolverTag(server["domain_resolver"]), visiting)
	}
	if requireDefault {
		route, _ := cfg["route"].(map[string]any)
		if !bootstrap(resolverTag(route["default_domain_resolver"]), map[string]bool{}) {
			return Reason("dns_bootstrap_unsupported")
		}
	}
	for _, server := range byTag {
		if server["type"] == "fakeip" {
			continue
		}
		host, _ := server["server"].(string)
		if addr, err := netip.ParseAddr(host); err == nil {
			if !addr.IsGlobalUnicast() || addr.IsLoopback() {
				return Reason("dns_bootstrap_unsupported")
			}
		} else {
			if host == "" || strings.ContainsAny(host, "/: ") || !bootstrap(resolverTag(server["domain_resolver"]), map[string]bool{}) {
				return Reason("dns_bootstrap_unsupported")
			}
		}
	}
	return nil
}
