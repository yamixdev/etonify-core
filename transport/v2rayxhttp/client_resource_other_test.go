//go:build !linux

package v2rayxhttp

import "testing"

func platformOpenFileDescriptorCount(*testing.T) int {
	return -1
}
