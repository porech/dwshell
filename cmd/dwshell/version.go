package main

import "github.com/porech/dwshell/internal/buildinfo"

// These are overridden at release time via -ldflags (goreleaser defaults):
//
//	-X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}
var (
	version = ""
	commit  = ""
	date    = ""
)

func versionString() string { return buildinfo.Format(version, commit, date) }
