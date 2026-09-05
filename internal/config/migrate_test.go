package config

import (
	"encoding/json"

	"github.com/porech/dwshell/internal/auth"
	"os"
	"path/filepath"
	"testing"
)

// flatConfigFixture builds the shape dwshell wrote before accounts existed,
// with the structure of a real file: a session carrying a genuine signing key
// and the node cookie, plus a trusted device with a key of its own. The keys
// are generated rather than invented, because they are parsed on load — a
// made-up JWK would fail for reasons that have nothing to do with migration.
func flatConfigFixture(t *testing.T) string {
	t.Helper()
	sessionKey, err := auth.NewSignKey()
	if err != nil {
		t.Fatal(err)
	}
	deviceKey, err := auth.NewSignKey()
	if err != nil {
		t.Fatal(err)
	}
	flat := map[string]any{
		"user": "ale@example.net",
		"session": map[string]any{
			"commandUrl":    "https://node1.dwservice.net/ses/ND1/tok.dw",
			"signKey":       sessionKey,
			"customHeaders": true,
			"cookies":       []map[string]string{{"name": "DWSID", "value": "abc"}},
		},
		"trustedDevice": map[string]any{
			"id":      "dev1",
			"name":    "dwshell on laptop",
			"authKey": deviceKey,
		},
	}
	b, err := json.Marshal(flat)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMigratesAFlatConfig(t *testing.T) {
	c, err := Load(writeConfig(t, flatConfigFixture(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Accounts) != 1 {
		t.Fatalf("expected one account, got %d", len(c.Accounts))
	}
	a := c.Accounts[0]
	if a.User != "ale@example.net" {
		t.Errorf("user = %q", a.User)
	}
	if a.Session == nil || a.Session.CommandURL == "" || a.Session.SignKey == nil {
		t.Error("the session must survive the migration whole")
	}
	if len(a.Session.Cookies) != 1 || a.Session.Cookies[0].Name != "DWSID" {
		t.Error("the node cookie must survive: without it the session cannot be reused")
	}
	if a.TrustedDevice == nil || a.TrustedDevice.ID != "dev1" {
		t.Error("the trusted device must survive, or passwordless refresh breaks")
	}
	if c.Default != "ale@example.net" {
		t.Errorf("the migrated account becomes the default, got %q", c.Default)
	}
}

// Migrating must not touch the file: a read-only command should never rewrite
// the user's configuration behind their back.
func TestLoadDoesNotRewriteTheFile(t *testing.T) {
	p := writeConfig(t, flatConfigFixture(t))
	before, _ := os.ReadFile(p)
	if _, err := Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("Load rewrote the configuration file")
	}
}

// Saving a migrated config writes the new shape and leaves the flat keys behind.
func TestSaveWritesTheAccountsShape(t *testing.T) {
	p := writeConfig(t, flatConfigFixture(t))
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var raw map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["accounts"]; !ok {
		t.Error("the saved file must carry accounts")
	}
	for _, gone := range []string{"user", "session", "trustedDevice"} {
		if _, ok := raw[gone]; ok {
			t.Errorf("the flat key %q must not be written back", gone)
		}
	}
	// and the credentials must still be there after the round trip
	again, err := Load(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(again.Accounts) != 1 || again.Accounts[0].TrustedDevice == nil {
		t.Fatal("the round trip lost the trusted device")
	}
}

func TestLoadOfANewShapeIsUnchanged(t *testing.T) {
	body := `{"default":"a@b","accounts":[{"user":"a@b"},{"user":"c@d"}]}`
	c, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Accounts) != 2 || c.Default != "a@b" {
		t.Fatalf("got %d accounts, default %q", len(c.Accounts), c.Default)
	}
}

func TestLoadOfAMissingFileIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Accounts) != 0 {
		t.Fatal("a missing file means no accounts, not an error")
	}
}
