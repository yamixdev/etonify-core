//go:build with_gvisor && with_quic && with_wireguard && with_utls

package libbox

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEtonifyAndroidConfigCorpus(t *testing.T) {
	outbounds := []any{
		map[string]any{
			"type":        "vless",
			"tag":         "vless-reality",
			"server":      "example.com",
			"server_port": 443,
			"uuid":        "7c6a5b3e-4f1a-4d2b-8c9e-1a2b3c4d5e6f",
			"flow":        "xtls-rprx-vision",
			"tls": map[string]any{
				"enabled":     true,
				"server_name": "example.com",
				"utls": map[string]any{
					"enabled":     true,
					"fingerprint": "chrome",
				},
				"reality": map[string]any{
					"enabled":    true,
					"public_key": "thwa3P0vSbbPNr0n94LqAzpFJGwTX3bpIlTyrIis7S8",
					"short_id":   "ab01",
					"spider_x":   "/assets?ed=2560",
				},
			},
		},
		map[string]any{
			"type":        "vmess",
			"tag":         "vmess-ws",
			"server":      "example.com",
			"server_port": 443,
			"uuid":        "7c6a5b3e-4f1a-4d2b-8c9e-1a2b3c4d5e6f",
			"security":    "auto",
			"tls":         etonifyTLS(),
			"transport": map[string]any{
				"type": "ws",
				"path": "/ws",
			},
		},
		map[string]any{
			"type":        "vmess",
			"tag":         "vmess-grpc",
			"server":      "example.com",
			"server_port": 443,
			"uuid":        "7c6a5b3e-4f1a-4d2b-8c9e-1a2b3c4d5e6f",
			"security":    "auto",
			"tls":         etonifyTLS(),
			"transport": map[string]any{
				"type":         "grpc",
				"service_name": "grpc",
			},
		},
		map[string]any{
			"type":        "trojan",
			"tag":         "trojan",
			"server":      "example.com",
			"server_port": 443,
			"password":    "test-password",
			"tls":         etonifyTLS(),
		},
		map[string]any{
			"type":        "shadowsocks",
			"tag":         "shadowsocks",
			"server":      "example.com",
			"server_port": 8388,
			"method":      "aes-128-gcm",
			"password":    "test-password",
		},
		map[string]any{
			"type":        "hysteria2",
			"tag":         "hysteria2",
			"server":      "example.com",
			"server_port": 443,
			"password":    "test-password",
			"tls":         etonifyTLS(),
		},
		map[string]any{
			"type":               "tuic",
			"tag":                "tuic",
			"server":             "example.com",
			"server_port":        443,
			"uuid":               "7c6a5b3e-4f1a-4d2b-8c9e-1a2b3c4d5e6f",
			"password":           "test-password",
			"congestion_control": "bbr",
			"tls":                etonifyTLS(),
		},
		map[string]any{
			"type":        "anytls",
			"tag":         "anytls",
			"server":      "example.com",
			"server_port": 443,
			"password":    "test-password",
			"tls":         etonifyTLS(),
		},
		etonifyDirect(),
	}
	selectable := []string{
		"vless-reality",
		"vmess-ws",
		"vmess-grpc",
		"trojan",
		"shadowsocks",
		"hysteria2",
		"tuic",
		"anytls",
		"wireguard",
	}
	outbounds = append([]any{map[string]any{
		"type":      "selector",
		"tag":       "select",
		"outbounds": selectable,
		"default":   "vless-reality",
	}}, outbounds...)
	config := etonifyConfig(
		[]any{etonifyTUNInbound("mixed"), etonifyMixedInbound()},
		outbounds,
	)
	config["endpoints"] = []any{
		map[string]any{
			"type":        "wireguard",
			"tag":         "wireguard",
			"address":     []string{"10.0.0.2/32"},
			"private_key": "yGXGKezPjPNbRfHAJNmkDDT4hPsYRFJ+/GIOQ1kzIXM=",
			"peers": []any{
				map[string]any{
					"address":     "example.com",
					"port":        51820,
					"public_key":  "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
					"allowed_ips": []string{"0.0.0.0/0", "::/0"},
				},
			},
		},
	}

	content, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, CheckConfig(string(content)))
}

func etonifyTLS() map[string]any {
	return map[string]any{
		"enabled":     true,
		"server_name": "example.com",
		"insecure":    true,
	}
}
