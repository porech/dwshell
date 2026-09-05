package config

import (
	"encoding/json"

	"github.com/porech/dwshell/internal/auth"
)

// flatConfig is the pre-accounts on-disk shape: one account's fields sitting at
// the top level, which is what every dwshell before multiple accounts wrote.
type flatConfig struct {
	User          string              `json:"user"`
	Session       *SessionState       `json:"session"`
	TrustedDevice *auth.TrustedDevice `json:"trustedDevice"`
}

// migrateFlat converts a pre-accounts configuration into a single account, in
// memory. It runs on every load and never writes: an untouched old file keeps
// working indefinitely, and the new shape reaches the disk only when something
// saves for a reason of its own. A read-only command must not rewrite the
// user's configuration behind their back.
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
