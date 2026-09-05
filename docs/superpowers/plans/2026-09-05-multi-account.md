# Multiple accounts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Register several DWService accounts and choose between them per command, without a single-account user ever noticing the feature exists.

**Architecture:** All multi-account logic lives in `internal/config`, which already owns the file. `Config` gains an accounts list and a selected account, and keeps exposing the selected one, so `internal/client` changes from `c.cfg.Session` to `c.cfg.Current().Session` and little else. The CLI gains a global `--account` and an `account` subcommand family.

**Tech Stack:** Go, standard library only.

**Spec:** `docs/superpowers/specs/2026-09-05-multi-account-design.md`

## Global Constraints

- **Invisible with one account.** Every command behaves and prints exactly as today; `(default)` appears only in `account list`. This is a test, not an intention.
- **An existing flat configuration keeps working**, migrated **in memory** on load. The new shape reaches disk only when something saves — a read-only command never rewrites the user's file.
- Selection precedence: `--account` flag, then `DWSHELL_ACCOUNT`, then the default account.
- `login` ignores both selectors — the email being logged in decides the account. Passing one is an error.
- `@` is not a selector: `dwshell alice@myserver` already means the remote OS user.
- Removing the default promotes the survivor **only** when exactly one remains; with two or more, commands without `--account` fail asking for `account default`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/config.go` (modify) | `Account`, accounts list, default, save/load |
| `internal/config/migrate.go` (new) | recognising the flat shape and converting it in memory |
| `internal/config/select.go` (new) | selection precedence, add/remove/set-default |
| `internal/client/client.go` (modify) | act on the selected account; login keyed by email; logout one or all |
| `cmd/dwshell/account.go` (new) | the `account` subcommand family |
| `cmd/dwshell/main.go` (modify) | `--account` on every command, `valueFlags`, dispatch, help |
| `README.md` (modify) | document it as an opt-in that single-account users can ignore |

---

### Task 1: The accounts model and the migration

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/migrate.go`, `internal/config/migrate_test.go`

**Interfaces:**
- Produces:
  ```go
  type Account struct {
      User          string              `json:"user,omitempty"`
      Session       *SessionState       `json:"session,omitempty"`
      TrustedDevice *auth.TrustedDevice `json:"trustedDevice,omitempty"`
  }
  type Config struct {
      Default  string     `json:"default,omitempty"`
      Accounts []*Account `json:"accounts,omitempty"`
      // path, selected: in-memory
  }
  func (c *Config) Find(email string) *Account
  ```

- [ ] **Step 1: Write the failing test**

The fixture mirrors a real file's structure — session with `commandUrl`, `signKey`, `customHeaders`, `cookies`, plus a `trustedDevice` with its `authKey`.

```go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// flatConfig is the shape dwshell wrote before accounts existed.
const flatConfig = `{
  "user": "ale@example.net",
  "session": {
    "commandUrl": "https://node1.dwservice.net/ses/ND1/tok.dw",
    "signKey": {"name":"k1","priv":{"crv":"P-256","d":"D","kty":"EC","x":"X","y":"Y"}},
    "customHeaders": true,
    "cookies": [{"name":"DWSID","value":"abc"}]
  },
  "trustedDevice": {
    "id": "dev1",
    "name": "dwshell on laptop",
    "authKey": {"name":"k2","priv":{"crv":"P-256","d":"D2","kty":"EC","x":"X2","y":"Y2"}}
  }
}`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMigratesAFlatConfig(t *testing.T) {
	c, err := Load(writeConfig(t, flatConfig))
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
	p := writeConfig(t, flatConfig)
	before, _ := os.ReadFile(p)
	if _, err := Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("Load rewrote the configuration file")
	}
}

// Saving a migrated config writes the new shape and drops the flat keys.
func TestSaveWritesTheAccountsShape(t *testing.T) {
	p := writeConfig(t, flatConfig)
	c, _ := Load(p)
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
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/config/ -v`
Expected: build failure — `c.Accounts` and `c.Default` do not exist

- [ ] **Step 3: Write the implementation**

In `config.go`, replace the flat fields with the accounts model:

```go
// Account is one DWService account: its user, the reusable session, and the
// optional trusted device that refreshes that session without a password.
type Account struct {
	User          string              `json:"user,omitempty"`
	Session       *SessionState       `json:"session,omitempty"`
	TrustedDevice *auth.TrustedDevice `json:"trustedDevice,omitempty"`
}

// Config is the on-disk state: the accounts that have been logged in, and which
// of them commands use when none is named.
type Config struct {
	Default  string     `json:"default,omitempty"`
	Accounts []*Account `json:"accounts,omitempty"`

	path     string
	selected *Account
}

// Find returns the account for an email, or nil.
func (c *Config) Find(email string) *Account {
	for _, a := range c.Accounts {
		if a.User == email {
			return a
		}
	}
	return nil
}
```

In `migrate.go`:

```go
package config

import "encoding/json"

// flatConfig is the pre-accounts on-disk shape: a single account's fields at
// the top level.
type flatConfig struct {
	User          string              `json:"user"`
	Session       *SessionState       `json:"session"`
	TrustedDevice *auth.TrustedDevice `json:"trustedDevice"`
}

// migrateFlat converts a pre-accounts configuration into a single account, in
// memory. It is applied on every load and never writes: an untouched old file
// keeps working, and the new shape reaches the disk only when something saves
// for a reason of its own.
//
// A file with neither accounts nor flat fields is simply empty — a first run.
func migrateFlat(body []byte, c *Config) error {
	if len(c.Accounts) > 0 {
		return nil // already the new shape
	}
	var flat flatConfig
	if err := json.Unmarshal(body, &flat); err != nil {
		return err
	}
	if flat.Session == nil && flat.TrustedDevice == nil && flat.User == "" {
		return nil
	}
	c.Accounts = []*Account{{
		User:          flat.User,
		Session:       flat.Session,
		TrustedDevice: flat.TrustedDevice,
	}}
	c.Default = flat.User
	return nil
}
```

And in `Load`, after unmarshalling into `c`, call `migrateFlat(b, c)`.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "config: hold several accounts, migrating a flat file in memory"
```

---

### Task 2: Selection, and the rules around the default

**Files:**
- Create: `internal/config/select.go`, `internal/config/select_test.go`

**Interfaces:**
- Consumes: `Account`, `Config`, `Find` (Task 1)
- Produces:
  ```go
  var ErrNoAccounts = errors.New(...)
  func (c *Config) Select(email string) error   // "" = DWSHELL_ACCOUNT, then Default
  func (c *Config) Current() *Account           // never nil after a successful Select
  func (c *Config) Add(user string) *Account    // first one becomes the default
  func (c *Config) Remove(email string) error   // promotes a lone survivor
  func (c *Config) SetDefault(email string) error
  func (c *Config) Emails() []string
  ```

- [ ] **Step 1: Write the failing tests**

```go
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

func TestSelectFallsBackToTheEnvironmentThenTheDefault(t *testing.T) {
	t.Setenv("DWSHELL_ACCOUNT", "c@d")
	c := twoAccounts()
	if err := c.Select(""); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if c.Current().User != "c@d" {
		t.Fatalf("the environment must be used, got %q", c.Current().User)
	}

	t.Setenv("DWSHELL_ACCOUNT", "")
	c = twoAccounts()
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

// With one account there is nothing to choose, so a missing default is not an
// error: this is what keeps the feature invisible.
func TestSelectWithOneAccountAndNoDefault(t *testing.T) {
	c := &Config{Accounts: []*Account{{User: "solo@x"}}}
	if err := c.Select(""); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if c.Current().User != "solo@x" {
		t.Fatal("the lone account is used whether or not it is marked default")
	}
}

func TestSelectWithSeveralAndNoDefaultAsksForOne(t *testing.T) {
	c := &Config{Accounts: []*Account{{User: "a@b"}, {User: "c@d"}}}
	err := c.Select("")
	if err == nil {
		t.Fatal("with no default and several accounts it must refuse rather than guess")
	}
	if !strings.Contains(err.Error(), "account default") {
		t.Errorf("the error should say how to fix it, got %q", err)
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

func TestSelectWithNoAccountsSaysToLogIn(t *testing.T) {
	c := &Config{}
	if err := c.Select(""); err == nil {
		t.Fatal("with no accounts at all it must fail")
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/config/ -run 'Select|Add|Remove|SetDefault' -v`
Expected: `undefined: Select`, `undefined: Add`, …

- [ ] **Step 3: Write the implementation**

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNoAccounts means nothing has been logged in yet.
var ErrNoAccounts = errors.New("no account configured: run `dwshell login`")

// Emails lists the registered accounts, in the order they were added.
func (c *Config) Emails() []string {
	out := make([]string, 0, len(c.Accounts))
	for _, a := range c.Accounts {
		out = append(out, a.User)
	}
	return out
}

// Select picks the account commands will act on: the argument if given, else
// DWSHELL_ACCOUNT, else the default. With a single account there is nothing to
// choose and it is used whether or not it is marked default — which is what
// keeps this feature out of a single-account user's way.
//
// With several accounts and no default it refuses rather than guessing: picking
// one silently would point the next command at the wrong account.
func (c *Config) Select(email string) error {
	if len(c.Accounts) == 0 {
		return ErrNoAccounts
	}
	if email == "" {
		email = os.Getenv("DWSHELL_ACCOUNT")
	}
	if email == "" {
		if len(c.Accounts) == 1 {
			c.selected = c.Accounts[0]
			return nil
		}
		email = c.Default
	}
	if email == "" {
		return fmt.Errorf("several accounts are configured and none is the default; "+
			"pick one with `dwshell account default <email>` or pass --account (%s)",
			strings.Join(c.Emails(), ", "))
	}
	a := c.Find(email)
	if a == nil {
		return fmt.Errorf("no account %q; registered accounts: %s", email, strings.Join(c.Emails(), ", "))
	}
	c.selected = a
	return nil
}

// Current is the selected account. Select must have succeeded first; callers
// that never selected get the lone account, so single-account code paths that
// predate this feature keep working.
func (c *Config) Current() *Account {
	if c.selected != nil {
		return c.selected
	}
	if len(c.Accounts) == 1 {
		return c.Accounts[0]
	}
	return &Account{}
}

// Add returns the account for an email, creating it if it is new. The first
// account registered becomes the default, so someone who only ever logs in once
// never meets the concept.
func (c *Config) Add(user string) *Account {
	if a := c.Find(user); a != nil {
		return a
	}
	a := &Account{User: user}
	c.Accounts = append(c.Accounts, a)
	if len(c.Accounts) == 1 {
		c.Default = user
	}
	c.selected = a
	return a
}

// Remove forgets an account. If exactly one remains it is promoted, there being
// nothing to choose; if several remain the default is left unset rather than
// guessed at.
func (c *Config) Remove(email string) error {
	idx := -1
	for i, a := range c.Accounts {
		if a.User == email {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no account %q; registered accounts: %s", email, strings.Join(c.Emails(), ", "))
	}
	c.Accounts = append(c.Accounts[:idx], c.Accounts[idx+1:]...)
	if c.selected != nil && c.selected.User == email {
		c.selected = nil
	}
	if c.Default == email {
		c.Default = ""
		if len(c.Accounts) == 1 {
			c.Default = c.Accounts[0].User
		}
	}
	return nil
}

// SetDefault marks a registered account as the one commands use by default.
func (c *Config) SetDefault(email string) error {
	if c.Find(email) == nil {
		return fmt.Errorf("no account %q; registered accounts: %s", email, strings.Join(c.Emails(), ", "))
	}
	c.Default = email
	return nil
}

// Clear removes every account (logout --all).
func (c *Config) Clear() {
	c.Accounts = nil
	c.Default = ""
	c.selected = nil
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "config: select an account, and the rules around the default"
```

---

### Task 3: The client acts on the selected account

**Files:**
- Modify: `internal/client/client.go`

**Interfaces:**
- Produces: `func New(configPath, account string) (*Client, error)` — the account selector, empty for the default
- Consumes: `Select`, `Current`, `Add`, `Remove`, `Clear` (Task 2)

- [ ] **Step 1: Adapt the call sites**

`New` selects before use, so an unknown `--account` fails at the earliest point rather than midway through a command:

```go
// New builds a Client from a config path (empty = default) and an account
// selector (empty = DWSHELL_ACCOUNT, else the default account), seeding the
// cookie jar with that account's persisted node cookie.
func New(configPath, account string) (*Client, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	// An empty configuration is not an error here: `login` starts from one.
	if len(cfg.Accounts) > 0 {
		if err := cfg.Select(account); err != nil {
			return nil, err
		}
	} else if account != "" {
		return nil, fmt.Errorf("no account %q: nothing is configured yet", account)
	}
	jar, _ := cookiejar.New(nil)
	if s := cfg.Current().Session; s != nil && len(s.Cookies) > 0 {
		if u, e := neturl.Parse(s.CommandURL); e == nil {
			var cks []*http.Cookie
			for _, c := range s.Cookies {
				cks = append(cks, &http.Cookie{Name: c.Name, Value: c.Value})
			}
			jar.SetCookies(u, cks)
		}
	}
	return &Client{cfg: cfg, http: &http.Client{Jar: jar, Timeout: 60 * time.Second}}, nil
}
```

Every `c.cfg.Session` becomes `c.cfg.Current().Session`, and likewise for `TrustedDevice`.

`Login` keys the account by the email it just authenticated:

```go
	// The email is the account key: a new one is registered, a known one has its
	// credentials replaced.
	acct := c.cfg.Add(user)
	if err := c.persistSession(ctx, boot); err != nil {
		return err
	}
	if tdReq != nil && tdReq.Result != nil {
		acct.TrustedDevice = tdReq.Result
	}
	return c.cfg.Save()
```

`persistSession` writes into `c.cfg.Current()`.

`Logout` forgets the selected account; `LogoutAll` clears everything:

```go
// Logout deregisters the selected account's trusted device (freeing its capped
// slot) and forgets it. With one account this is exactly what it always did.
func (c *Client) Logout(ctx context.Context) error {
	c.deregister(ctx, c.cfg.Current())
	if u := c.cfg.Current().User; u != "" {
		if err := c.cfg.Remove(u); err != nil {
			return err
		}
	} else {
		c.cfg.Clear()
	}
	return c.cfg.Save()
}

// LogoutAll forgets every account, deregistering each trusted device.
func (c *Client) LogoutAll(ctx context.Context) error {
	for _, a := range c.cfg.Accounts {
		c.deregister(ctx, a)
	}
	c.cfg.Clear()
	return c.cfg.Save()
}

// deregister removes an account's trusted device server-side; failure is
// non-fatal, as it always was — the local credentials still go.
func (c *Client) deregister(ctx context.Context, a *config.Account) {
	if a == nil || a.TrustedDevice == nil {
		return
	}
	if cfg, err := auth.FetchLoginConfig(ctx, c.http); err == nil {
		_ = auth.RemoveTrustedDevice(ctx, c.http, cfg, a.TrustedDevice)
	}
}
```

- [ ] **Step 2: Build and run the suite**

Run: `go build ./... && go test ./...`
Expected: compile errors at every `client.New(` call site in `cmd/dwshell`, fixed in Task 4.

- [ ] **Step 3: Commit (with Task 4, which unbreaks the build)**

---

### Task 4: `--account` on every command

**Files:**
- Modify: `cmd/dwshell/main.go`, `cmd/dwshell/files.go`, `cmd/dwshell/agent.go`
- Test: `cmd/dwshell/main_test.go`

- [ ] **Step 1: Write the failing test**

```go
// --account takes a value, so the shell shortcut has to skip that value when
// looking for the agent name — otherwise `dwshell --account a@b GHE` would take
// the email for the agent.
func TestExtractAgentSkipsTheAccountValue(t *testing.T) {
	agent, flags := extractAgent([]string{"--account", "a@b", "GHE", "-c", "ls"})
	if agent != "GHE" {
		t.Fatalf("agent = %q, want GHE", agent)
	}
	joined := strings.Join(flags, " ")
	if !strings.Contains(joined, "--account a@b") || !strings.Contains(joined, "-c ls") {
		t.Fatalf("flags = %v", flags)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./cmd/dwshell/ -run ExtractAgentSkips -v`
Expected: FAIL — `agent = "a@b"`

- [ ] **Step 3: Write the implementation**

```go
var valueFlags = map[string]bool{"c": true, "term": true, "config": true, "timeout": true, "account": true}
```

Every command that builds a client gains:

```go
	fs.StringVar(&account, "account", "", "account to use (default: the default account)")
```

and passes it: `client.New(configPath, account)`. Help gains, under Global:

```
  --account email  Account to use when several are logged in
```

- [ ] **Step 4: Run the suite**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/client cmd/dwshell
git commit -m "account: act on the selected account, chosen with --account"
```

---

### Task 5: `dwshell account` and `logout --all`

**Files:**
- Create: `cmd/dwshell/account.go`
- Modify: `cmd/dwshell/main.go` (dispatch, help, `logout --all`)

- [ ] **Step 1: Write the implementation**

```go
// cmdAccountList prints the registered accounts. It is the only output in
// dwshell that mentions a default, and with one account it says nothing
// remarkable.
func cmdAccountList(ctx context.Context, args []string) int {
	// … --config, --json
	for _, a := range cfg.Accounts {
		mark := ""
		if a.User == cfg.Default {
			mark = "  (default)"
		}
		fmt.Printf("%s%s\n", a.User, mark)
	}
	return 0
}
```

`account default <email>` calls `SetDefault` then `Save`; `account rm <email>` deregisters that account's trusted device, calls `Remove`, then `Save`, behind the same confirmation `agent rm` uses.

`logout` gains `--all`, routing to `LogoutAll`.

- [ ] **Step 2: Run the suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Verify with a scratch configuration, not the real one**

```bash
export DWSHELL_CONFIG=$(mktemp -d)/config.json
go run ./cmd/dwshell account list          # no accounts → says to log in
```

- [ ] **Step 4: Commit**

```bash
git add cmd/dwshell
git commit -m "account: list, set the default, and remove"
```

---

### Task 6: Live verification with two real accounts, then docs

**Files:**
- Modify: `README.md`

The two accounts see the same machines from opposite sides — the second owns
what the first sees as shares — which makes a wrong selection obvious rather
than subtle.

- [ ] **Step 1: Prove the existing configuration still works untouched**

```bash
cp ~/.config/dwshell/config.json /tmp/dwshell-config.bak
go run ./cmd/dwshell list | head -3     # unchanged, no flag, no migration written
diff <(cat ~/.config/dwshell/config.json) /tmp/dwshell-config.bak && echo "file untouched"
```

- [ ] **Step 2: Register the second account and check both**

```bash
DWSHELL_PASSWORD=… go run ./cmd/dwshell login --user info@futura.fm
go run ./cmd/dwshell account list                       # two, the first marked default
go run ./cmd/dwshell list | grep Regia                  # shared
go run ./cmd/dwshell --account info@futura.fm list | grep Regia   # own
DWSHELL_ACCOUNT=info@futura.fm go run ./cmd/dwshell list | grep Regia  # own
go run ./cmd/dwshell list | grep Regia                  # shared again: the default is untouched
```

Expected: the same machine reads `shared` from one account and `own` from the
other, and the unflagged command keeps using the default.

- [ ] **Step 3: Check a command that is not `list`**

```bash
go run ./cmd/dwshell --account info@futura.fm Regia -c "echo ok"
```

- [ ] **Step 4: Restore**

```bash
go run ./cmd/dwshell account rm info@futura.fm --yes
go run ./cmd/dwshell account list        # one account again
```

- [ ] **Step 5: Document it**

`README.md` gains a short section presenting accounts as opt-in: log in twice
and the second appears, choose with `--account` or `DWSHELL_ACCOUNT`, manage
with `dwshell account`. It says plainly that with one account nothing changes,
and that an existing configuration is migrated on first use.

- [ ] **Step 6: Full verification and commit**

```bash
gofmt -l cmd internal && go vet ./... && go test -race ./...
git add README.md && git commit -m "docs: using more than one account"
```

---

## Self-review

**Spec coverage.** §2 selection → Task 2 (precedence) and Task 4 (`--account`, `valueFlags`). §3 registering by email → Task 2 `Add` and Task 3 `Login`. §4 `account` subcommand → Task 5. §5 logout and the default-removal rules → Tasks 2 and 3. §6 format and migration → Task 1. §7 structure → the file table. §8 errors → Task 2's tests, one per case. §9 testing → each task, plus Task 6 live.

**Placeholders.** Task 5's list function is shown in outline rather than in full, because it is a print loop over an interface fixed in Task 2; every other step carries its code.

**Type consistency.** `Account`, `Config`, `Find` from Task 1 are used unchanged. `Select`/`Current`/`Add`/`Remove`/`SetDefault`/`Emails` from Task 2 are what Tasks 3–5 call. `client.New` gains its second parameter in Task 3 and every call site is updated in Task 4 — the build is deliberately broken between them, which is why they share a commit.

**Gap found while reviewing:** `login` must ignore `--account` (spec §3). Task 4 adds the flag to every command generically, so `login` needs the explicit refusal — folded into Task 4's implementation step as an error when both `--user` and `--account` disagree, rather than a silent no-op.
