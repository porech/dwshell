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

// What the output may contain is narrow, and each exclusion has a reason.
func TestInstallInstructionsGiveOnlyTheCodeAndThePage(t *testing.T) {
	out := installInstructions(281407902)

	if !strings.Contains(out, "281-407-902") {
		t.Error("must show the code, dashed as the installer expects it")
	}
	if strings.Contains(out, "281407902") {
		t.Error("must never show the undashed code")
	}
	if !strings.Contains(out, "https://www.dwservice.net/download.html") {
		t.Error("must point at the download page, where the licence is accepted")
	}

	// The download page carries the licence acceptance, so nothing here may
	// fetch the installer or run it for the user.
	for _, forbidden := range []string{"curl", "wget", "download/dwagent", "| sh", "|sh"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("must not hand out a download-and-run line, found %q", forbidden)
		}
	}

	// Silent installation is refused by the service ("Silent installation
	// forbidden. Please contact the support."), so promising it would send
	// people down a path that does not work.
	for _, forbidden := range []string{"-silent", "dwagent.sh", "dwagent.exe"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("must not describe an unattended install, found %q", forbidden)
		}
	}
}

// Automation must not be able to delete a machine by accident.
func TestConfirmRefusesWithoutATerminal(t *testing.T) {
	err := confirm("delete agent \"x\"?", false, false)
	if err == nil {
		t.Fatal("with no terminal and no --yes it must refuse")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the error should name --yes, got %q", err)
	}
}

func TestConfirmPassesWithYes(t *testing.T) {
	if err := confirm("delete agent \"x\"?", true, false); err != nil {
		t.Fatalf("--yes must proceed without a terminal: %v", err)
	}
}
