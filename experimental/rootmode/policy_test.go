package rootmode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRootRoutingPrecedesAndroidExplicitNetworkRules(t *testing.T) {
	data, err := AdaptConfig([]byte(`{"inbounds":[{"type":"tun","iproute2_rule_index":18000,"auto_redirect_iproute2_fallback_rule_index":18020}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	tun := cfg["inbounds"].([]any)[0].(map[string]any)
	start := int(tun["iproute2_rule_index"].(float64))
	fallback := int(tun["auto_redirect_iproute2_fallback_rule_index"].(float64))
	// Android 15 RouteController: lockdown must win, but explicit network
	// selection must not route app traffic before our TUN fallback.
	for _, priority := range []int{start - 1, start, start + 1, start + 2, fallback} {
		if priority <= 14000 || priority >= 15000 {
			t.Errorf("root priority %d must follow lockdown (14000) and precede UID explicit network (15000)", priority)
		}
	}
	if fallback <= start+2 {
		t.Fatal("fallback must follow output-mark goto target")
	}
}

func TestConfigPolicyForcesOwnedNativeTun(t *testing.T) {
	data, err := AdaptConfig([]byte(`{"inbounds":[{"type":"tun","stack":"system","interface_name":"evil","address":["172.19.0.1/30"]}],"outbounds":[{"type":"direct","tag":"direct"}],"dns":{"servers":[{"type":"udp","tag":"dns","server":"1.1.1.1"}]},"route":{}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	tun := cfg["inbounds"].([]any)[0].(map[string]any)
	if tun["interface_name"] != "etonify0" || tun["auto_route"] != true || tun["auto_redirect"] != true || tun["iproute2_table_index"] != float64(20239) || tun["iproute2_rule_index"] != float64(rootRulePriority) {
		t.Fatalf("unowned tun: %v", tun)
	}
	if _, ok := tun["stack"]; ok {
		t.Fatal("legacy stack retained")
	}
	if cfg["route"].(map[string]any)["auto_detect_interface"] != true {
		t.Fatal("missing native route detection")
	}
}

func TestConfigPolicyRejectsPrivilegedInputs(t *testing.T) {
	for _, extra := range []string{
		`"services":[{"type":"ssm-api"}]`, `"endpoints":[{"type":"tailscale"}]`,
		`"experimental":{"clash_api":{"external_controller":"0.0.0.0:9090"}}`,
		`"log":{"output":"/data/system/secret"}`, `"certificate":{"certificate_path":["/etc/secret"]}`,
		`"outbounds":[{"type":"ssh","tag":"x"}]`,
		`"outbounds":[{"type":"direct","tag":"x","bind_interface":"eth0"}]`,
		`"outbounds":[{"type":"trojan","tag":"x","tls":{"certificate_path":"/etc/secret"}}]`,
		`"outbounds":[{"type":"trojan","tag":"x","tls":{"CERTIFICATE_PATH":"/etc/secret"}}]`,
		`"outbounds":[{"TYPE":"ssh","tag":"x"}]`,
		`"outbounds":[{"type":"shadowsocks","plugin":"v2ray-plugin","plugin_opts":"tls;cert=/etc/secret"}]`,
		`"dns":{"servers":[{"type":"local","tag":"dns-local"}]}`,
	} {
		t.Run(extra, func(t *testing.T) {
			_, err := AdaptConfig([]byte(`{"inbounds":[{"type":"tun"}],`+extra+`}`), nil)
			if err == nil {
				t.Fatal("unsafe config accepted")
			}
			if strings.Contains(err.Error(), "/etc/secret") || strings.Contains(err.Error(), "/data/system") {
				t.Fatal("secret path in error")
			}
		})
	}
}

func TestConfigPolicyRejectsPackageSplitAndMultipleListeners(t *testing.T) {
	for _, inbounds := range []string{`[{"type":"tun","exclude_package":["com.bank"]}]`, `[{"type":"tun"},{"type":"mixed","listen":"127.0.0.1","listen_port":8080}]`, `[]`} {
		if _, err := AdaptConfig([]byte(`{"inbounds":`+inbounds+`}`), nil); err == nil {
			t.Fatal("unsupported routing accepted")
		}
	}
}

func TestConfigPolicySnapshotsOnlyRouteRulePaths(t *testing.T) {
	calls := 0
	data, err := AdaptConfig([]byte(`{"inbounds":[{"type":"tun"}],"route":{"rule_set":[{"type":"local","tag":"r","path":"/data/user/0/com.etonify.meow_client/files/route-data/r.srs"}]},"outbounds":[{"type":"vless","transport":{"type":"ws","path":"/ws"}}]}`), func(path string) (string, error) { calls++; return "/data/adb/etonify/runtime/rules/0.srs", nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(string(data), `"path":"/ws"`) || !strings.Contains(string(data), "runtime/rules/0.srs") {
		t.Fatalf("bad snapshot: %s", data)
	}
}

func TestRequestBoundaryRejectsNonRootAndForeignUID(t *testing.T) {
	if RequireRoot(1000) == nil || RequireRoot(0) != nil {
		t.Fatal("root gate bypassed")
	}
	for _, uid := range []uint32{0, 1000, 10002} {
		if PeerAllowed(uid, 10001) {
			t.Fatalf("foreign peer %d", uid)
		}
	}
	if !PeerAllowed(10001, 10001) {
		t.Fatal("app peer denied")
	}
	if AllowedRPC("/daemon.ManagedService/TriggerDebugCrash") || AllowedRPC("/daemon.StartedService/StartTailscaleSSHSession") || AllowedRPC("/daemon.StartedService/DeleteTaildropFile") {
		t.Fatal("dangerous RPC allowed")
	}
	if !AllowedRPC("/daemon.StartedService/URLTest") || !AllowedRPC("/daemon.ManagedService/StopService") {
		t.Fatal("app RPC denied")
	}
}

func TestConfigFrameBounds(t *testing.T) {
	if _, err := ReadConfigFrame(strings.NewReader("{}\nlease"), 2); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"{}", "{} \n", "\n"} {
		if _, err := ReadConfigFrame(strings.NewReader(input), 2); err == nil {
			t.Fatalf("bad frame %q accepted", input)
		}
	}
}
