package rootmode

import "testing"

func TestRuntimeNeverFallsBackToDeviceResolver(t *testing.T) {
	for _, config := range []string{`{}`, `{"dns":{"servers":[{"type":"udp","tag":"bootstrap","server":"192.168.1.1"}]}}`, `{"dns":{"servers":[{"type":"udp","tag":"bootstrap","server":"192.168.1.1","detour":"select"}]},"route":{"default_domain_resolver":"bootstrap"}}`} {
		if validateRuntimeDNS([]byte(config)) == nil {
			t.Fatalf("implicit resolver accepted: %s", config)
		}
	}
	if err := validateRuntimeDNS([]byte(`{"dns":{"servers":[{"type":"udp","tag":"bootstrap","server":"192.168.1.1"}]},"route":{"default_domain_resolver":"bootstrap"}}`)); err != nil {
		t.Fatal(err)
	}
}
