package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadEtonifyVersion(t *testing.T) {
	versionPath := filepath.Join(t.TempDir(), "ETONIFY_VERSION")
	if err := os.WriteFile(versionPath, []byte("v1.14.0-etonify.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	version, found, err := readEtonifyVersion(versionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected Etonify version file to be detected")
	}
	if version != "1.14.0-etonify.1" {
		t.Fatalf("unexpected embedded version: %q", version)
	}
}

func TestReadEtonifyVersionMissing(t *testing.T) {
	version, found, err := readEtonifyVersion(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if found || version != "" {
		t.Fatalf("unexpected missing-file result: version=%q found=%v", version, found)
	}
}

func TestReadEtonifyVersionRejectsInvalidContent(t *testing.T) {
	for _, content := range []string{"\n", "v1.14.0 etonify.1"} {
		t.Run(content, func(t *testing.T) {
			versionPath := filepath.Join(t.TempDir(), "ETONIFY_VERSION")
			if err := os.WriteFile(versionPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, found, err := readEtonifyVersion(versionPath); !found || err == nil {
				t.Fatalf("expected invalid version to fail: found=%v err=%v", found, err)
			}
		})
	}
}
