package libbox

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEtonifyConfigCorpus(t *testing.T) {
	tests := map[string]map[string]any{
		"local proxy": etonifyConfig(
			[]any{etonifyMixedInbound()},
			[]any{etonifySelector(), etonifyDirect()},
		),
		"system TUN and local proxy": etonifyConfig(
			[]any{etonifyTUNInbound("system"), etonifyMixedInbound()},
			[]any{etonifySelector(), etonifyDirect()},
		),
		"gVisor TUN": etonifyConfig(
			[]any{etonifyTUNInbound("gvisor")},
			[]any{etonifySelector(), etonifyDirect()},
		),
		"mixed TUN": etonifyConfig(
			[]any{etonifyTUNInbound("mixed")},
			[]any{etonifySelector(), etonifyDirect()},
		),
		"typed DNS presets": etonifyDNSPresetConfig(),
		"FakeIP route":      etonifyFakeIPConfig(),
		"XHTTP and VLESS Encryption": etonifyConfig(
			nil,
			[]any{
				map[string]any{
					"type":      "selector",
					"tag":       "select",
					"outbounds": []string{"encrypted-xhttp"},
					"default":   "encrypted-xhttp",
				},
				map[string]any{
					"type":        "vless",
					"tag":         "encrypted-xhttp",
					"server":      "example.com",
					"server_port": 443,
					"uuid":        "7c6a5b3e-4f1a-4d2b-8c9e-1a2b3c4d5e6f",
					"encryption":  "mlkem768x25519plus.native.0rtt.MDG42I0GTLyH5a6fuXipicFe-A_m-FHNYyJGkheQJTs",
					"transport": map[string]any{
						"type": "xhttp",
						"path": "/api",
						"mode": "stream-up",
					},
				},
				etonifyDirect(),
			},
		),
	}

	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			content, err := json.Marshal(config)
			require.NoError(t, err)
			require.NoError(t, CheckConfig(string(content)))
		})
	}
}

func TestEtonifyLegacyIndependentDNSCacheRemainsAcceptedDuringUpgrade(t *testing.T) {
	config := etonifyConfig(nil, []any{etonifySelector(), etonifyDirect()})
	dnsOptions := config["dns"].(map[string]any)
	dnsOptions["independent_cache"] = true
	content, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, CheckConfig(string(content)))
}

func etonifyConfig(inbounds []any, outbounds []any) map[string]any {
	return map[string]any{
		"log": map[string]any{"level": "panic"},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"type": "local", "tag": "dns-local"},
			},
			"final":          "dns-local",
			"cache_capacity": 4096,
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"auto_detect_interface":   true,
			"default_domain_resolver": "dns-local",
			"final":                   "select",
		},
	}
}

func etonifySelector() map[string]any {
	return map[string]any{
		"type":      "selector",
		"tag":       "select",
		"outbounds": []string{"direct"},
		"default":   "direct",
	}
}

func etonifyDirect() map[string]any {
	return map[string]any{"type": "direct", "tag": "direct"}
}

func etonifyMixedInbound() map[string]any {
	return map[string]any{
		"type":        "mixed",
		"tag":         "mixed-in",
		"listen":      "127.0.0.1",
		"listen_port": 1080,
		"users": []any{
			map[string]any{"username": "meow", "password": "test-password"},
		},
	}
}

func etonifyTUNInbound(stack string) map[string]any {
	return map[string]any{
		"type":         "tun",
		"tag":          "tun-in",
		"address":      []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
		"mtu":          3400,
		"auto_route":   true,
		"strict_route": true,
		"stack":        stack,
	}
}

func etonifyDNSPresetConfig() map[string]any {
	config := etonifyConfig(nil, []any{etonifySelector(), etonifyDirect()})
	config["dns"] = map[string]any{
		"servers": []any{
			map[string]any{"type": "local", "tag": "dns-local"},
			map[string]any{"type": "udp", "tag": "dns-udp", "server": "1.1.1.1", "server_port": 53},
			map[string]any{"type": "tcp", "tag": "dns-tcp", "server": "8.8.8.8", "server_port": 53},
			map[string]any{
				"type":            "tls",
				"tag":             "dns-tls",
				"server":          "dns.google",
				"server_port":     853,
				"domain_resolver": "dns-local",
			},
			map[string]any{
				"type":            "https",
				"tag":             "dns-https",
				"server":          "dns.cloudflare.com",
				"server_port":     443,
				"path":            "/dns-query",
				"domain_resolver": "dns-local",
			},
		},
		"final":          "dns-https",
		"cache_capacity": 4096,
	}
	return config
}

func etonifyFakeIPConfig() map[string]any {
	config := etonifyConfig(
		[]any{etonifyTUNInbound("system")},
		[]any{etonifySelector(), etonifyDirect()},
	)
	config["dns"] = map[string]any{
		"servers": []any{
			map[string]any{"type": "local", "tag": "dns-local"},
			map[string]any{
				"type":        "fakeip",
				"tag":         "dns-fakeip",
				"inet4_range": "198.18.0.0/15",
				"inet6_range": "fc00::/18",
			},
		},
		"rules": []any{
			map[string]any{
				"query_type": []string{"A", "AAAA"},
				"action":     "route",
				"server":     "dns-fakeip",
			},
		},
		"final":          "dns-local",
		"cache_capacity": 4096,
	}
	return config
}
