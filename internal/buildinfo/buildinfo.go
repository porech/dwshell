// Package buildinfo derives a human-readable version string for the binary.
//
// Precedence:
//  1. values injected at build time via -ldflags (set by goreleaser on a release);
//  2. otherwise Go's embedded VCS build info (module version for `go install
//     …@vX`, or the commit + a "-dirty" marker for a plain `go build` in a repo);
//  3. otherwise "dev".
package buildinfo

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// pseudoVersionRe matches Go pseudo-versions (they embed a 14-digit timestamp),
// which we treat as "dev" rather than a real release tag.
var pseudoVersionRe = regexp.MustCompile(`[0-9]{14}`)

// Format builds the version string from ldflags-provided values, falling back to
// the embedded build info when they are empty.
func Format(version, commit, date string) string {
	if version != "" {
		out := version
		if commit != "" {
			out += " (" + shortCommit(commit) + ")"
		}
		if date != "" {
			out += " " + date
		}
		return out
	}
	return fromBuildInfo()
}

func fromBuildInfo() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}

	var rev, ts string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		case "vcs.time":
			ts = s.Value
		}
	}

	// A tagged install (`go install module@vX.Y.Z`) carries a real Main.Version;
	// plain builds and untagged installs carry "(devel)" or a pseudo-version,
	// which we normalize to "dev" (the commit is appended below).
	base := bi.Main.Version
	if base == "" || base == "(devel)" || strings.Contains(base, "+") || pseudoVersionRe.MatchString(base) {
		base = "dev"
	}

	out := base
	if rev != "" {
		out += " (" + shortCommit(rev)
		if dirty {
			out += "-dirty"
		}
		out += ")"
	} else if dirty {
		out += " (dirty)"
	}
	if ts != "" {
		out += " " + ts
	}
	return out
}

func shortCommit(c string) string {
	c = strings.TrimSpace(c)
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
