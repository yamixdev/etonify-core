package rootmode

import (
	"strings"
	"testing"
)

func TestDNSHostnameRequiresExplicitAcyclicNumericBootstrap(t *testing.T) {
	for _, servers := range []string{
		`[{"type":"https","tag":"remote","server":"dns.example","path":"/dns-query","domain_resolver":"bootstrap"},{"type":"udp","tag":"bootstrap","server":"192.168.1.1"}]`,
		`[{"type":"https","tag":"remote","server":"dns.example","path":"/dns-query","domain_resolver":{"server":"bootstrap"}},{"type":"udp","tag":"bootstrap","server":"192.168.1.1"}]`,
	} {
		data, err := AdaptConfig([]byte(`{"inbounds":[{"type":"tun"}],"dns":{"servers":`+servers+`}}`), nil)
		if err != nil || !strings.Contains(string(data), "dns-query") {
			t.Fatalf("safe bootstrap rejected: %v", err)
		}
	}
	for _, servers := range []string{
		`[{"type":"https","tag":"remote","server":"dns.example"}]`,
		`[{"type":"https","tag":"remote","server":"dns.example","domain_resolver":"remote"}]`,
		`[{"type":"https","tag":"remote","server":"dns.example","domain_resolver":"b"},{"type":"https","tag":"b","server":"b.example","domain_resolver":"remote"}]`,
		`[{"type":"udp","tag":"b","server":"127.0.0.1"}]`,
		`[{"type":"local","tag":"b"}]`,
	} {
		if _, err := AdaptConfig([]byte(`{"inbounds":[{"type":"tun"}],"dns":{"servers":`+servers+`}}`), nil); err == nil {
			t.Fatal("unsafe resolver graph accepted")
		}
	}
}
