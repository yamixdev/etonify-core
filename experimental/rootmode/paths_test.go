package rootmode

import "testing"

func TestRulePathPolicyStaysInFixedPrivateStaging(t *testing.T) {
	good := "/data/user/0/com.etonify.meow_client/files/root-rule-staging/test.srs"
	if relative, err := RuleRelativePath(good, 10123); err != nil || relative != "root-rule-staging/test.srs" {
		t.Fatalf("%s %v", relative, err)
	}
	for _, path := range []string{"/data/system/packages.xml", "/data/user/0/com.other/files/root-rule-staging/x", "/data/user/0/com.etonify.meow_client/files/root-rule-staging/../secret", "/data/user/0/com.etonify.meow_client/files/root-rule-staging2/x", "/data/user/0/com.etonify.meow_client/files/root-rule-staging/a//b", "/data/user/0/com.etonify.meow_client/files/private.key", good} {
		uid := 10123
		if path == good {
			uid = 110123
		}
		if _, err := RuleRelativePath(path, uid); err == nil {
			t.Fatalf("foreign file allowed: %s uid%d", path, uid)
		}
	}
}

func TestOwnershipRejectsOtherClientChains(t *testing.T) {
	for _, name := range []string{"sing-box-output", "etonify-not-owned", "etonify-include-abc", "OTHER"} {
		if OwnsChain(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"etonify-output", "etonify-prerouting", "etonify-cont-123", "etonify-local-0"} {
		if !OwnsChain(name) {
			t.Fatal(name)
		}
	}
}
