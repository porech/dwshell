# Multiple accounts (`--account`, `dwshell account …`) — design

Date: 2026-09-05
Status: approved for implementation

## 1. Purpose

Use dwshell with more than one DWService account — say a personal one and a
work one — choosing between them per command, the way the AWS CLI chooses a
profile.

Two requirements shape everything below, and they are the reason this is not
simply "add a profiles list":

- **Someone who does not want this must never learn it exists.** With one
  account every command behaves exactly as it does today, prints exactly what it
  prints today, and the word "default" appears nowhere. The feature only starts
  meaning something when a second account is registered.
- **An existing configuration keeps working**, migrated rather than discarded.

## 2. Selecting an account

```
--account <email>        one invocation
DWSHELL_ACCOUNT=<email>  a shell session, a script
```

The flag wins over the variable, the variable over the default. An unknown
account is an error naming the ones that are registered — the same shape as
`agent group` against an unknown group.

`--account` is a value flag, so it must join `valueFlags` for the shell shortcut
to keep parsing (`dwshell --account a@b GHE -c "…"`).

`@` is not available as a selector: `dwshell alice@myserver` already means the
remote OS user, and overloading it would make `dwshell ale@alerinaldi.it` mean
two different things depending on whether the left side happens to name an
account.

## 3. Registering: the email is the key

`dwshell login` keys the account by the email that was logged in.

- an email not seen before → a new account is added
- an email already registered → its credentials are replaced, which is exactly
  today's behaviour
- the **first** account registered becomes the default, so a single-account user
  never meets the concept

`login` ignores `--account` and `DWSHELL_ACCOUNT`: which account it touches is
decided by the email being logged in, not by a selector. Passing one anyway is
an error rather than a silent no-op, since it can only mean a misunderstanding.

## 4. `dwshell account`

```
dwshell account list [--json]      registered accounts, marking the default
dwshell account default <email>    change the default
dwshell account rm <email>         remove one, deregistering its trusted device
```

`account list` prints one account per line — the email, and `(default)` against
the default one — or the same as a JSON array under `--json`. It is the only
place the word "default" appears in output, and with one account it is
unremarkable.

## 5. `logout`

Acts on the **selected** account: deregisters its trusted device and removes it.
With one account that is precisely today's behaviour. `--all` clears every
account, for the person who wants a clean slate.

`logout` and `account rm` do the same thing to a single account; `logout` acts
on the selected one, `account rm` on a named one.

### Removing the default

If exactly one account remains, it is promoted silently — there is nothing to
choose. If two or more remain, dwshell does **not** pick one: commands without
`--account` fail saying which accounts exist and to set one with
`dwshell account default`. Guessing here would silently point a later command at
the wrong account, which is worse than an error.

## 6. On-disk format and migration

```json
{
  "default": "ale@example.net",
  "accounts": [
    { "user": "ale@example.net", "session": {…}, "trustedDevice": {…} },
    { "user": "info@example.com", "session": {…} }
  ]
}
```

Today's file is flat — `{"user":…, "session":{…}, "trustedDevice":{…}}` — and is
recognised on load and converted **in memory** into a single account, which
becomes the default. The new shape reaches the disk only when something actually
saves. A read-only command never rewrites the file behind the user's back, and
an untouched old configuration keeps working indefinitely.

A configuration holding a session but no user name (possible in principle,
though `login` always records one) migrates to an account listed as `(unnamed)`;
it stays usable as the default, and logging in again names it.

The migration is tested against a real configuration file's shape, not an
invented one — session with `commandUrl`, `signKey` (name + JWK private key),
`customHeaders`, `cookies`, plus `trustedDevice` with `id`, `name`, `authKey`.

`--config` is unchanged: it still names the one file.

## 7. Structure

The multi-account logic stays in `internal/config`, which is where the file
already lives. `Config` grows the accounts list and a notion of which one is
selected, and keeps exposing the selected account's fields, so its consumers
barely change:

```go
type Account struct {
    User          string              `json:"user,omitempty"`
    Session       *SessionState       `json:"session,omitempty"`
    TrustedDevice *auth.TrustedDevice `json:"trustedDevice,omitempty"`
}

type Config struct {
    Default  string     `json:"default,omitempty"`
    Accounts []*Account `json:"accounts,omitempty"`
    // path and the selected account are in-memory only
}

func (c *Config) Select(email string) error  // "" = env, then default
func (c *Config) Current() *Account          // never nil once Select succeeded
func (c *Config) Add(user string) *Account
func (c *Config) Remove(email string) error
func (c *Config) SetDefault(email string) error
```

`internal/client` changes from `c.cfg.Session` to `c.cfg.Current().Session` and
takes the selected account through `client.New`. `cmd/dwshell/account.go` holds
the subcommand family, as `agent.go` and `files.go` do.

## 8. Errors

- unknown `--account` → refuse, listing registered accounts
- no accounts at all → the existing "run `dwshell login`" message, unchanged
- several accounts and no default → refuse, saying to set one
- `account rm` of an account that is not registered → refuse, listing them
- `account default` of an unregistered account → refuse, listing them

## 9. Testing

Unit tests in `internal/config`: migration from the flat shape (built from a
real file's structure), selection precedence (flag over environment over
default), first-account-becomes-default, removing the default with one and with
several remaining, and that saving a migrated config produces the new shape
without losing the trusted device.

The invisibility requirement gets its own test: with exactly one account, a
round trip through load and save leaves behaviour and output unchanged.

Live verification with two real accounts, both of the user's: registering the
second, running the same command against each, and confirming the first still
works with no flag.

## 10. Open question

The second account's credentials are the user's to supply. Registering it live
means `dwshell login --user info@futura.fm` with its password and whatever
second factor it has — which only they can provide.
