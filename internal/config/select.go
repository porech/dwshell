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
// DWSHELL_ACCOUNT, else the default.
//
// With a single account there is nothing to choose, and it is used whether or
// not it is marked default — which is what keeps this out of the way of someone
// who only ever logs in once. With several accounts and no default it refuses
// rather than guessing: picking one silently would point the next command at
// the wrong account.
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
			"choose one with `dwshell account default <email>` or pass --account (%s)",
			strings.Join(c.Emails(), ", "))
	}
	a := c.Find(email)
	if a == nil {
		return fmt.Errorf("no account %q; registered accounts: %s", email, strings.Join(c.Emails(), ", "))
	}
	c.selected = a
	return nil
}

// Current is the selected account. When nothing was selected it falls back to a
// lone account, so the single-account paths that predate this feature behave
// exactly as they did.
func (c *Config) Current() *Account {
	if c.selected != nil {
		return c.selected
	}
	if len(c.Accounts) == 1 {
		return c.Accounts[0]
	}
	return &Account{}
}

// Add returns the account for an email, registering it if it is new, and
// selects it. The first account registered becomes the default, so a
// single-account user never meets the concept.
func (c *Config) Add(user string) *Account {
	if a := c.Find(user); a != nil {
		c.selected = a
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
// nothing to choose between; if several remain the default is left unset rather
// than guessed at.
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

// SetDefault marks a registered account as the one commands use when none is named.
func (c *Config) SetDefault(email string) error {
	if c.Find(email) == nil {
		return fmt.Errorf("no account %q; registered accounts: %s", email, strings.Join(c.Emails(), ", "))
	}
	c.Default = email
	return nil
}

// Clear forgets every account (logout --all).
func (c *Config) Clear() {
	c.Accounts = nil
	c.Default = ""
	c.selected = nil
}
