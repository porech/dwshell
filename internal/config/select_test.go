package config

import (
	"strings"
	"testing"
)

func twoAccounts() *Config {
	return &Config{
		Default:  "a@b",
		Accounts: []*Account{{User: "a@b"}, {User: "c@d"}},
	}
}

func TestSelectPrefersTheFlagOverTheEnvironment(t *testing.T) {
	t.Setenv("DWSHELL_ACCOUNT", "c@d")
	c := twoAccounts()
	if err := c.Select("a@b"); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if c.Current().User != "a@b" {
		t.Fatalf("the flag must win, got %q", c.Current().User)
	}
}

func TestSelectFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv("DWSHELL_ACCOUNT", "c@d")
	c := twoAccounts()
	if err := c.Select(""); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if c.Current().User != "c@d" {
		t.Fatalf("the environment must be used, got %q", c.Current().User)
	}
}

func TestSelectFallsBackToTheDefault(t *testing.T) {
	t.Setenv("DWSHELL_ACCOUNT", "")
	c := twoAccounts()
	if err := c.Select(""); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if c.Current().User != "a@b" {
		t.Fatalf("the default must be used, got %q", c.Current().User)
	}
}

func TestSelectUnknownAccountListsTheKnownOnes(t *testing.T) {
	c := twoAccounts()
	err := c.Select("nope@x")
	if err == nil {
		t.Fatal("an unknown account must fail")
	}
	for _, want := range []string{"a@b", "c@d"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list %q, got %q", want, err)
		}
	}
}

// With one account there is nothing to choose, so it is used whether or not it
// is marked default. This is what keeps the feature invisible to someone who
// only ever logs in once.
func TestSelectWithOneAccountAndNoDefault(t *testing.T) {
	t.Setenv("DWSHELL_ACCOUNT", "")
	c := &Config{Accounts: []*Account{{User: "solo@x"}}}
	if err := c.Select(""); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if c.Current().User != "solo@x" {
		t.Fatal("the lone account is used whether or not it is marked default")
	}
}

func TestSelectWithSeveralAndNoDefaultAsksForOne(t *testing.T) {
	t.Setenv("DWSHELL_ACCOUNT", "")
	c := &Config{Accounts: []*Account{{User: "a@b"}, {User: "c@d"}}}
	err := c.Select("")
	if err == nil {
		t.Fatal("with no default and several accounts it must refuse rather than guess")
	}
	if !strings.Contains(err.Error(), "account default") {
		t.Errorf("the error should say how to fix it, got %q", err)
	}
}

func TestSelectWithNoAccountsSaysToLogIn(t *testing.T) {
	c := &Config{}
	if err := c.Select(""); err == nil {
		t.Fatal("with no accounts at all it must fail")
	}
}

func TestAddMakesTheFirstAccountTheDefault(t *testing.T) {
	c := &Config{}
	c.Add("first@x")
	if c.Default != "first@x" {
		t.Fatalf("default = %q, want first@x", c.Default)
	}
	c.Add("second@x")
	if c.Default != "first@x" {
		t.Fatal("a later account must not steal the default")
	}
	if len(c.Accounts) != 2 {
		t.Fatalf("got %d accounts", len(c.Accounts))
	}
}

func TestAddIsIdempotentForTheSameEmail(t *testing.T) {
	c := &Config{}
	a1 := c.Add("same@x")
	a2 := c.Add("same@x")
	if a1 != a2 || len(c.Accounts) != 1 {
		t.Fatal("logging in again with the same email updates that account")
	}
}

func TestRemovePromotesALoneSurvivor(t *testing.T) {
	c := twoAccounts()
	if err := c.Remove("a@b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if c.Default != "c@d" {
		t.Fatalf("with one account left it becomes the default, got %q", c.Default)
	}
}

func TestRemoveLeavesNoDefaultWhenSeveralRemain(t *testing.T) {
	c := &Config{Default: "a@b", Accounts: []*Account{{User: "a@b"}, {User: "c@d"}, {User: "e@f"}}}
	if err := c.Remove("a@b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if c.Default != "" {
		t.Fatalf("dwshell must not pick a default among several, got %q", c.Default)
	}
}

func TestRemoveUnknownAccountFails(t *testing.T) {
	if err := twoAccounts().Remove("nope@x"); err == nil {
		t.Fatal("removing an unregistered account must fail")
	}
}

func TestSetDefaultRejectsAnUnregisteredAccount(t *testing.T) {
	if err := twoAccounts().SetDefault("nope@x"); err == nil {
		t.Fatal("the default must name a registered account")
	}
}
