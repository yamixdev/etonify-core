package tun

import "testing"

func TestShouldBindTunForwarderToInterface(t *testing.T) {
	tests := []struct {
		name                 string
		isDarwin             bool
		isAndroid            bool
		hasPlatformInterface bool
		expected             bool
	}{
		{
			name:     "darwin",
			isDarwin: true,
			expected: true,
		},
		{
			name:                 "android libbox",
			isAndroid:            true,
			hasPlatformInterface: true,
			expected:             true,
		},
		{
			name:      "android command line",
			isAndroid: true,
			expected:  false,
		},
		{
			name:                 "other platform interface",
			hasPlatformInterface: true,
			expected:             false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := shouldBindTunForwarderToInterface(
				test.isDarwin,
				test.isAndroid,
				test.hasPlatformInterface,
			)
			if actual != test.expected {
				t.Fatalf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}
