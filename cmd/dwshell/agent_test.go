package main

import (
	"strings"
	"testing"
)

// The code is shown and typed in groups of three, the way the web client
// renders it and the way the installer forwards it.
func TestFormatCodeGroupsInThrees(t *testing.T) {
	if got := formatCode(281407902); got != "281-407-902" {
		t.Fatalf("formatCode = %q, want 281-407-902", got)
	}
}

// tempCode travels as a JSON number, so a leading zero is already gone by the
// time we see it; padding restores the nine digits the grouping assumes.
func TestFormatCodePadsToNineDigits(t *testing.T) {
	if got := formatCode(12345678); got != "012-345-678" {
		t.Fatalf("formatCode = %q, want 012-345-678", got)
	}
}

// The download page is where the licence is accepted, so dwshell must never
// hand out a line that fetches the installer and runs it.
func TestInstallInstructionsNeverGiveADownloadAndRunLine(t *testing.T) {
	out := installInstructions(281407902)
	if !strings.Contains(out, "https://www.dwservice.net/download.html") {
		t.Error("must point at the download page, where the licence is accepted")
	}
	for _, forbidden := range []string{"curl", "wget", "download/dwagent", "| sh", "|sh"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("must not hand out a download-and-run line, found %q", forbidden)
		}
	}
	if !strings.Contains(out, "-silent key=281-407-902") {
		t.Error("must show the silent-install line with the dashed code")
	}
	if strings.Contains(out, "key=281407902") {
		t.Error("must never hand out the undashed code")
	}
}
