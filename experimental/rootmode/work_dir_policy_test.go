package rootmode

import "testing"

func TestWorkDirAcceptsAndroidSystemDataOwner(t *testing.T) {
	for _, c := range []struct {
		part           string
		uid, gid, mode uint32
	}{
		{"data", 1000, 1000, 0771}, {"data", 0, 0, 0755},
		{"adb", 0, 0, 0700}, {"etonify", 0, 0, 0700}, {"runtime", 0, 0, 0700},
	} {
		if !workDirComponentAllowed(c.part, c.uid, c.gid, c.mode) {
			t.Fatalf("trusted directory rejected: %+v", c)
		}
	}
}

func TestWorkDirRejectsUntrustedOwnersAndWritableAncestors(t *testing.T) {
	for _, c := range []struct {
		part           string
		uid, gid, mode uint32
	}{
		{"data", 10001, 1000, 0771}, {"data", 1000, 1000, 0777},
		{"data", 0, 10001, 0770}, {"adb", 1000, 1000, 0700},
		{"adb", 0, 10001, 0770}, {"etonify", 1000, 1000, 0700},
		{"etonify", 0, 0, 0755}, {"runtime", 0, 0, 0710}, {"runtime", 10001, 0, 0700},
		{"unknown", 0, 0, 0700},
	} {
		if workDirComponentAllowed(c.part, c.uid, c.gid, c.mode) {
			t.Fatalf("untrusted directory accepted: %+v", c)
		}
	}
}
