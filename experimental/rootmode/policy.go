package rootmode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const Protocol = 1
const ModuleVersion = "0.1.0"
const MaxConfigBytes = 16 << 20

type Reason string

func (r Reason) Error() string { return string(r) }

func AdaptConfig(data []byte, snapshot func(string) (string, error)) ([]byte, error) {
	var cfg map[string]any
	if len(data) > MaxConfigBytes || json.Unmarshal(data, &cfg) != nil || cfg == nil {
		return nil, Reason("config_invalid")
	}
	for key := range cfg {
		switch key {
		case "log", "dns", "inbounds", "outbounds", "route", "experimental":
		default:
			return nil, Reason("config_unsupported")
		}
	}
	if err := inspect(cfg, ""); err != nil {
		return nil, err
	}
	if err := validateDNS(cfg); err != nil {
		return nil, err
	}
	inbounds, ok := cfg["inbounds"].([]any)
	if !ok || len(inbounds) != 1 {
		return nil, Reason("tun_required")
	}
	in, ok := inbounds[0].(map[string]any)
	if !ok || in["type"] != "tun" {
		return nil, Reason("tun_required")
	}
	for _, key := range []string{"include_package", "exclude_package", "include_uid", "exclude_uid", "include_uid_range", "exclude_uid_range", "include_android_user", "include_interface", "exclude_interface", "include_mac_address", "exclude_mac_address"} {
		if value, exists := in[key]; exists {
			list, ok := value.([]any)
			if !ok || len(list) > 0 {
				return nil, Reason("package_split_unsupported")
			}
			delete(in, key)
		}
	}
	for _, key := range []string{"stack", "platform"} {
		delete(in, key)
	}
	in["interface_name"] = "etonify0"
	in["auto_route"] = true
	in["auto_redirect"] = true
	in["iproute2_table_index"] = rootRouteTable
	in["iproute2_rule_index"] = rootRulePriority
	in["auto_redirect_iproute2_fallback_rule_index"] = rootFallbackPriority
	in["auto_redirect_input_mark"] = fmt.Sprintf("%#x", rootInputMark)
	in["auto_redirect_output_mark"] = fmt.Sprintf("%#x", rootOutputMark)
	in["auto_redirect_reset_mark"] = fmt.Sprintf("%#x", rootResetMark)
	in["auto_redirect_tproxy_mark"] = fmt.Sprintf("%#x", rootTProxyMark)
	in["auto_redirect_nfqueue"] = 239
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	route["auto_detect_interface"] = true
	if rules, ok := route["rule_set"].([]any); ok {
		for _, value := range rules {
			rule, ok := value.(map[string]any)
			if !ok || rule["type"] != "local" || snapshot == nil {
				return nil, Reason("rule_file_unsupported")
			}
			path, ok := rule["path"].(string)
			if !ok || path == "" {
				return nil, Reason("rule_file_unsupported")
			}
			owned, err := snapshot(path)
			if err != nil {
				return nil, Reason("rule_file_denied")
			}
			rule["path"] = owned
		}
	}
	return json.Marshal(cfg)
}

func inspect(value any, location string) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.HasSuffix(location, "/headers") {
				if err := inspect(child, location+"/*"); err != nil {
					return err
				}
				continue
			}
			if key != strings.ToLower(key) {
				return Reason("config_unsupported")
			}
			next := location + "/" + key
			if location == "/experimental" && key != "clash_api" {
				return Reason("config_unsupported")
			}
			if location == "/experimental/clash_api" && key != "default_mode" {
				return Reason("config_unsupported")
			}
			if location == "/log" && key == "output" {
				return Reason("file_access_denied")
			}
			if location == "/dns/servers/*" && key == "address" {
				return Reason("dns_local_unsupported")
			}
			if strings.Contains(key, "path") || strings.Contains(key, "directory") || strings.Contains(key, "file") {
				allowed := key == "path" && (location == "/route/rule_set/*" || strings.HasSuffix(location, "/transport") || location == "/dns/servers/*" && (value["type"] == "https" || value["type"] == "h3"))
				if !allowed && key != "tcp_multi_path" && key != "disable_path_mtu_discovery" {
					return Reason("file_access_denied")
				}
			}
			switch key {
			case "netns", "network_namespace", "network_namespaces", "protect_path", "bind_interface", "inet4_bind_address", "inet6_bind_address", "routing_mark", "listen", "listen_port", "external_controller", "external_ui", "external_ui_download_url", "certificate_provider", "acme", "command", "script", "detour_env", "plugin", "plugin_opts":
				return Reason("config_unsupported")
			}
			if key == "type" {
				kind, _ := child.(string)
				if location == "/outbounds/*" {
					switch kind {
					case "direct", "block", "selector", "urltest", "socks", "http", "shadowsocks", "snell", "vmess", "trojan", "shadowtls", "vless", "anytls", "hysteria", "hysteria2", "tuic":
					default:
						return Reason("outbound_unsupported")
					}
				}
				if location == "/dns/servers/*" {
					switch kind {
					case "udp", "tcp", "tls", "https", "quic", "h3", "fakeip":
					default:
						return Reason("dns_local_unsupported")
					}
				}
			}
			if err := inspect(child, next); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := inspect(child, location+"/*"); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(value, "${") || strings.Contains(value, "$env{") || strings.IndexByte(value, 0) >= 0 {
			return Reason("environment_expansion_denied")
		}
	}
	return nil
}

func RequireRoot(uid int) error {
	if uid != 0 {
		return Reason("root_required")
	}
	return nil
}
func PeerAllowed(uid, appUID uint32) bool { return appUID >= 10000 && uid == appUID }
func AllowedRPC(method string) bool {
	if method == "/daemon.ManagedService/StopService" || method == "/grpc.health.v1.Health/Check" {
		return true
	}
	name, ok := strings.CutPrefix(method, "/daemon.StartedService/")
	if !ok {
		return false
	}
	switch name {
	case "GetVersion", "SubscribeServiceStatus", "SubscribeLog", "GetDefaultLogLevel", "SubscribeStatus", "SubscribeGroups", "GetClashModeStatus", "SubscribeClashMode", "SetClashMode", "URLTest", "SubscribeURLTestUpdates", "CancelURLTest", "LookupOutboundExternalInfo", "FetchURLViaOutbound", "SelectOutbound", "SetGroupExpand", "SubscribeConnections", "CloseConnection", "CloseAllConnections", "GetDeprecatedWarnings", "GetStartedAt", "SubscribeOutbounds", "SubscribeNotifications":
		return true
	}
	return false
}

func ReadConfigFrame(reader io.Reader, limit int) ([]byte, error) {
	result := make([]byte, 0, 4096)
	one := make([]byte, 1)
	for len(result) <= limit {
		n, err := reader.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				if len(result) == 0 {
					return nil, Reason("config_invalid")
				}
				return result, nil
			}
			result = append(result, one[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, Reason("config_frame_incomplete")
			}
			return nil, Reason("config_read_failed")
		}
	}
	return nil, Reason("config_too_large")
}
