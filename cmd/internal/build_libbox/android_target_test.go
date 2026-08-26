package main

import "testing"

func TestResolveAndroidBindTargetDefaultsToArm(t *testing.T) {
	if target := resolveAndroidBindTarget("", false); target != "android/arm,android/arm64" {
		t.Fatalf("unexpected default Android target: %s", target)
	}
}

func TestResolveAndroidBindTargetHonorsExplicitTarget(t *testing.T) {
	if target := resolveAndroidBindTarget("android/arm64", false); target != "android/arm64" {
		t.Fatalf("unexpected explicit Android target: %s", target)
	}
}

func TestResolveAndroidBindTargetKeepsDebugBuildSmall(t *testing.T) {
	if target := resolveAndroidBindTarget("", true); target != "android/arm64" {
		t.Fatalf("unexpected debug Android target: %s", target)
	}
}
