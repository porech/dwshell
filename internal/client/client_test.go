package client

import "testing"

func TestDeviceNameFor(t *testing.T) {
	tests := []struct{ host, want string }{
		{"mybox", "dwshell on mybox"},
		{"MacBook-Pro.local", "dwshell on MacBook-Pro.local"},
		{"  spaced  ", "dwshell on spaced"},
		{"", "dwshell"},
		{"localhost", "dwshell"},
		{"LOCALHOST", "dwshell"},
	}
	for _, tc := range tests {
		if got := deviceNameFor(tc.host); got != tc.want {
			t.Errorf("deviceNameFor(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}
