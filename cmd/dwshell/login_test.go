package main

import "testing"

// asking records what the prompt was offered as a default, and answers with a
// canned reply, standing in for a person at a terminal.
func asking(answer string, seen *string) func(string) string {
	return func(suggested string) string {
		*seen = suggested
		return answer
	}
}

func TestLoginUserPrefersTheFlag(t *testing.T) {
	var asked string
	got := loginUser("typed@x", "default@x", true, asking("ignored@x", &asked))
	if got != "typed@x" {
		t.Fatalf("user = %q, want the --user value", got)
	}
	if asked != "" {
		t.Error("with --user given there is nothing to ask")
	}
}

// The whole point: at a terminal the email is asked, so a bare `dwshell login`
// can register an account that is not the default one.
func TestLoginUserAsksSoAnAccountCanBeAdded(t *testing.T) {
	var asked string
	got := loginUser("", "default@x", true, asking("second@x", &asked))
	if got != "second@x" {
		t.Fatalf("user = %q: a new email typed at the prompt must be honoured", got)
	}
	if asked != "default@x" {
		t.Errorf("the prompt should offer the default account, offered %q", asked)
	}
}

// Answering nothing accepts the default, so refreshing the account you already
// have stays a single keystroke.
func TestLoginUserEmptyAnswerKeepsTheDefault(t *testing.T) {
	var asked string
	got := loginUser("", "default@x", true, asking("", &asked))
	if got != "default@x" {
		t.Fatalf("user = %q, want the default", got)
	}
}

// With no terminal there is nobody to ask, so a script refreshing the default
// account keeps working exactly as before.
func TestLoginUserFallsBackToTheDefaultWithoutATerminal(t *testing.T) {
	var asked string
	got := loginUser("", "default@x", false, asking("unused@x", &asked))
	if got != "default@x" {
		t.Fatalf("user = %q, want the default", got)
	}
	if asked != "" {
		t.Error("nothing may be asked when there is no terminal")
	}
}

func TestLoginUserAsksWithNothingConfigured(t *testing.T) {
	var asked string
	got := loginUser("", "", true, asking("first@x", &asked))
	if got != "first@x" {
		t.Fatalf("user = %q", got)
	}
	if asked != "" {
		t.Errorf("with no account there is no default to offer, offered %q", asked)
	}
}
