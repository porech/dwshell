// Package config persists dwshell credentials: the reusable account session and
// the optional permanent trusted-device token. The file is created mode 0600.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/porech/dwshell/internal/auth"
)

// NamedCookie is a persisted cookie (the node's DWSID session cookie).
type NamedCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SessionState is a persisted account session, reused across invocations until
// it expires. The node sets a DWSID cookie that binds the session, so it is
// stored alongside the URL and signing key.
type SessionState struct {
	CommandURL    string        `json:"commandUrl"`
	SignKey       *auth.SignKey `json:"signKey"`
	CustomHeaders bool          `json:"customHeaders"`
	Cookies       []NamedCookie `json:"cookies,omitempty"`
}

// Config is the on-disk state.
type Config struct {
	User          string              `json:"user,omitempty"`
	Session       *SessionState       `json:"session,omitempty"`
	TrustedDevice *auth.TrustedDevice `json:"trustedDevice,omitempty"`

	path string
}

// DefaultPath returns the config file path (XDG on Unix, AppData on Windows),
// honoring DWSHELL_CONFIG when set.
func DefaultPath() string {
	if p := os.Getenv("DWSHELL_CONFIG"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("AppData"); dir != "" {
			return filepath.Join(dir, "dwshell", "config.json")
		}
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "dwshell", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dwshell", "config.json")
}

// Load reads the config from path, returning an empty (non-nil) config if the
// file does not exist.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	c := &Config{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	c.path = path
	return c, nil
}

// Save writes the config atomically with mode 0600.
func (c *Config) Save() error {
	if c.path == "" {
		c.path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Path returns the file path backing this config.
func (c *Config) Path() string { return c.path }

// Clear removes all persisted credentials (used by logout).
func (c *Config) Clear() {
	c.Session = nil
	c.TrustedDevice = nil
}
