package main

import "testing"

func TestParseRemote(t *testing.T) {
	tests := []struct {
		in         string
		host, path string
		ok         bool
	}{
		{"GHE:/etc/hostname", "GHE", "/etc/hostname", true},
		{"GHE:C:\\Users\\me", "GHE", "C:\\Users\\me", true}, // first colon splits; Windows remote path survives
		{"myhost:/", "myhost", "/", true},
		{"/local/path", "", "", false}, // no colon
		{":/nohost", "", "", false},    // empty host
		{"host:", "", "", false},       // empty path
	}
	for _, tc := range tests {
		host, p, err := parseRemote(tc.in)
		if tc.ok {
			if err != nil || host != tc.host || p != tc.path {
				t.Errorf("parseRemote(%q) = (%q,%q,%v), want (%q,%q,nil)", tc.in, host, p, err, tc.host, tc.path)
			}
		} else if err == nil {
			t.Errorf("parseRemote(%q) expected error, got (%q,%q)", tc.in, host, p)
		}
	}
}

func TestSplitPositional(t *testing.T) {
	pos, flags := splitPositional([]string{"local.txt", "--config", "/tmp/c.json", "GHE:/remote", "--own"})
	if len(pos) != 2 || pos[0] != "local.txt" || pos[1] != "GHE:/remote" {
		t.Fatalf("positionals wrong: %v", pos)
	}
	// --config consumes its value; --own is a bare bool flag.
	if len(flags) != 3 {
		t.Fatalf("flags wrong: %v", flags)
	}
}

func TestNormalizeRemotePath(t *testing.T) {
	// Windows remote: backslashes become slashes (interchangeable).
	if got := normalizeRemotePath(`C:\Windows\System32`, 1 /*OSWindows*/); got != "C:/Windows/System32" {
		t.Errorf("windows: got %q", got)
	}
	// *nix remote: backslash is a literal char, left untouched.
	if got := normalizeRemotePath(`/home/a\b`, 0 /*OSLinux*/); got != `/home/a\b` {
		t.Errorf("linux: got %q", got)
	}
}
