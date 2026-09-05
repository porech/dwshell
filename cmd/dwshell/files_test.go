package main

import "testing"

func TestParseRemote(t *testing.T) {
	tests := []struct {
		in          string
		agent, path string
		ok          bool
	}{
		{"GHE:/etc/hostname", "GHE", "/etc/hostname", true},
		{"GHE:C:\\Users\\me", "GHE", "C:\\Users\\me", true}, // first colon splits; Windows remote path survives
		{"myhost:/", "myhost", "/", true},
		{"/local/path", "", "", false}, // no colon
		{":/nohost", "", "", false},    // empty agent
		{"agent:", "agent", "", true},  // empty path means the remote root
	}
	for _, tc := range tests {
		agent, p, err := parseRemote(tc.in)
		if tc.ok {
			if err != nil || agent != tc.agent || p != tc.path {
				t.Errorf("parseRemote(%q) = (%q,%q,%v), want (%q,%q,nil)", tc.in, agent, p, err, tc.agent, tc.path)
			}
		} else if err == nil {
			t.Errorf("parseRemote(%q) expected error, got (%q,%q)", tc.in, agent, p)
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

func TestCanonicalRemotePath(t *testing.T) {
	const linux, windows = 0, 1 // remote.OSLinux, remote.OSWindows

	nix := map[string]string{
		"/etc/hostname": "/etc/hostname",
		"tmp":           "/tmp", // relative -> anchored to root
		"foo/bar":       "/foo/bar",
		"":              "/", // empty -> root
		"/":             "/",
		`/home/a\b`:     `/home/a\b`, // '\' is a literal filename char on *nix
	}
	for in, want := range nix {
		if got := canonicalRemotePath(in, linux); got != want {
			t.Errorf("nix canonicalRemotePath(%q) = %q, want %q", in, got, want)
		}
	}

	win := map[string]string{
		`C:\Windows\System32`: "C:/Windows/System32", // '\' -> '/'
		"C:/Windows":          "C:/Windows",
		"/C:/Windows":         "C:/Windows", // leading slash before a drive is stripped
		"/C:":                 "C:/",        // bare drive -> drive root
		"C:":                  "C:/",
		"":                    "$", // root -> drive list
		"/":                   "$",
		`\`:                   "$",
	}
	for in, want := range win {
		if got := canonicalRemotePath(in, windows); got != want {
			t.Errorf("win canonicalRemotePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsRemoteEndpoint(t *testing.T) {
	cases := map[string]bool{
		"GHE:/etc":       true,
		"GHE:C:\\Users":  true,
		`C:\Users`:       false, // local Windows drive
		"C:/Users":       false,
		"/local/path":    false,
		"./rel":          false,
		"agent:relative": true,
	}
	for in, want := range cases {
		if got := isRemoteEndpoint(in); got != want {
			t.Errorf("isRemoteEndpoint(%q) = %v, want %v", in, got, want)
		}
	}
}
