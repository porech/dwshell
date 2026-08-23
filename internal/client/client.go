// Package client is the high-level facade tying auth, config, session, and
// remote together. It is UI-agnostic: the CLI and any future UI use it to log
// in, obtain a valid account session, and connect to machines.
package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"time"

	"github.com/porech/dwshell/internal/auth"
	"github.com/porech/dwshell/internal/config"
	"github.com/porech/dwshell/internal/remote"
	"github.com/porech/dwshell/internal/session"
)

// ErrNeedLogin means no valid session and no trusted device: the user must run
// `dwshell login`.
var ErrNeedLogin = errors.New("authentication required: run `dwshell login`")

// Client wraps configuration and an HTTP client.
type Client struct {
	cfg  *config.Config
	http *http.Client
}

// New builds a Client from a config path (empty = default), seeding the cookie
// jar with any persisted node session cookie.
func New(configPath string) (*Client, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	if s := cfg.Session; s != nil && len(s.Cookies) > 0 {
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

// Config exposes the underlying config (e.g. for the user name).
func (c *Client) Config() *config.Config { return c.cfg }

// Login authenticates with a password and persists the session. It handles a
// second factor (TOTP or email) via twoFA when the server requests one. Unless
// registerTrusted is false, it also registers and stores one trusted device so
// future sessions can refresh without a password.
func (c *Client) Login(ctx context.Context, user, password string, registerTrusted bool, twoFA auth.TwoFactorFunc) error {
	// Start from a clean jar so a stale session cookie is not mixed with the new
	// login's cookies (which would send two DWSID values and break reuse).
	if jar, err := cookiejar.New(nil); err == nil {
		c.http.Jar = jar
	}
	cfg, err := auth.FetchLoginConfig(ctx, c.http)
	if err != nil {
		return err
	}

	var tdReq *auth.TrustedDeviceRequest
	if registerTrusted {
		tdReq = auth.NewTrustedDeviceRequest("dwshell")
	}
	boot, err := auth.LoginPassword(ctx, c.http, cfg, user, password, tdReq, twoFA)
	if err != nil {
		return err
	}

	if err := c.persistSession(ctx, boot); err != nil {
		return err
	}
	c.cfg.User = user
	if tdReq != nil && tdReq.Result != nil {
		c.cfg.TrustedDevice = tdReq.Result
	}
	return c.cfg.Save()
}

// Logout deregisters the trusted device on the account (freeing its capped slot)
// and forgets local credentials. Server-side removal failure is non-fatal.
func (c *Client) Logout(ctx context.Context) error {
	if td := c.cfg.TrustedDevice; td != nil {
		if cfg, err := auth.FetchLoginConfig(ctx, c.http); err == nil {
			_ = auth.RemoveTrustedDevice(ctx, c.http, cfg, td)
		}
	}
	c.cfg.Clear()
	c.cfg.User = ""
	return c.cfg.Save()
}

// Session returns a valid account session, refreshing via the trusted device if
// the stored session has expired. Returns ErrNeedLogin when neither is usable.
func (c *Client) Session(ctx context.Context) (*session.Session, error) {
	if s := c.cfg.Session; s != nil && s.SignKey != nil {
		sess := session.Restore(s.CommandURL, s.SignKey, s.CustomHeaders, c.http)
		if sess.Valid(ctx) {
			return sess, nil
		}
	}
	// Session missing/expired: try a passwordless refresh.
	if td := c.cfg.TrustedDevice; td != nil {
		cfg, err := auth.FetchLoginConfig(ctx, c.http)
		if err != nil {
			return nil, err
		}
		boot, err := auth.LoginTrustedDevice(ctx, c.http, cfg, td)
		if err != nil {
			return nil, fmt.Errorf("%w (trusted-device refresh failed: %v)", ErrNeedLogin, err)
		}
		if err := c.persistSession(ctx, boot); err != nil {
			return nil, err
		}
		if err := c.cfg.Save(); err != nil {
			return nil, err
		}
		return session.Restore(c.cfg.Session.CommandURL, c.cfg.Session.SignKey, c.cfg.Session.CustomHeaders, c.http), nil
	}
	return nil, ErrNeedLogin
}

// persistSession initializes the account session from a bootstrap and stores it.
func (c *Client) persistSession(ctx context.Context, boot *auth.Bootstrap) error {
	url, _ := boot.Raw["url"].(string)
	if url == "" {
		return fmt.Errorf("login bootstrap missing session url")
	}
	sess := session.New(url, boot.SignKey, c.http)
	if err := sess.Initialize(ctx); err != nil {
		return err
	}
	c.cfg.Session = &config.SessionState{
		CommandURL:    sess.CommandURL(),
		SignKey:       boot.SignKey,
		CustomHeaders: sess.CustomHeaders(),
		Cookies:       c.nodeCookies(sess.CommandURL()),
	}
	return nil
}

// nodeCookies snapshots the jar's cookies for the node host, for persistence.
func (c *Client) nodeCookies(commandURL string) []config.NamedCookie {
	u, err := neturl.Parse(commandURL)
	if err != nil || c.http.Jar == nil {
		return nil
	}
	// Dedupe by name (last wins) so a single DWSID is persisted.
	seen := map[string]int{}
	var out []config.NamedCookie
	for _, ck := range c.http.Jar.Cookies(u) {
		if i, ok := seen[ck.Name]; ok {
			out[i].Value = ck.Value
			continue
		}
		seen[ck.Name] = len(out)
		out = append(out, config.NamedCookie{Name: ck.Name, Value: ck.Value})
	}
	return out
}

// List returns the machines reachable from the current session.
func (c *Client) List(ctx context.Context) ([]remote.Machine, error) {
	sess, err := c.Session(ctx)
	if err != nil {
		return nil, err
	}
	ms, err := remote.List(ctx, sess)
	if err != nil {
		return nil, err
	}
	remote.SortMachines(ms)
	return ms, nil
}

// Connect resolves a machine by name/id and opens a session to its agent.
func (c *Client) Connect(ctx context.Context, query string, filter remote.Filter) (*remote.Machine, *session.Session, error) {
	sess, err := c.Session(ctx)
	if err != nil {
		return nil, nil, err
	}
	ms, err := remote.List(ctx, sess)
	if err != nil {
		return nil, nil, err
	}
	m, err := remote.Resolve(ms, query, filter)
	if err != nil {
		return nil, nil, err
	}
	if !m.Online {
		return m, nil, fmt.Errorf("%s is not online", m.Name)
	}
	if !m.SupportsShell() {
		return m, nil, fmt.Errorf("%s does not offer the shell app", m.Name)
	}
	agentSess, err := remote.Connect(ctx, sess, m, c.http)
	if err != nil {
		return m, nil, err
	}
	return m, agentSess, nil
}
