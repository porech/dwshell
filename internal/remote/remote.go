// Package remote lists and connects to DWService machines (owned agents and
// incoming shares) over an authenticated account session. It is app-agnostic:
// connecting yields a session on which any app (shell, files, desktop) can run.
package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/porech/dwshell/internal/auth"
	"github.com/porech/dwshell/internal/session"
)

// OS classifies the remote operating system.
type OS int

const (
	OSLinux   OS = 0
	OSWindows OS = 1
	OSMac     OS = 2
)

func (o OS) String() string {
	switch o {
	case OSLinux:
		return "Linux"
	case OSWindows:
		return "Windows"
	case OSMac:
		return "Mac"
	default:
		return "Unknown"
	}
}

// IsUnix reports whether the remote uses a Unix-like shell (affects TERM export
// and the -c exit-code sentinel).
func (o OS) IsUnix() bool { return o == OSLinux || o == OSMac }

// Machine is an owned agent or an incoming share.
type Machine struct {
	Name    string
	ID      string // agent id (owned) or share idAgent (shared)
	ShareID string // share _id (shares only), used with idAgent to connect
	OS      OS
	Online  bool
	Shared  bool
	Owner   string   // share owner display name
	Group   string   // optional group label
	Apps    []string // supported applications

	// Pending is an agent created but never installed. The service reports it
	// as state "W" with a null osType, so it is neither online nor meaningfully
	// typed — it is waiting for someone to run the installer with InstallCode.
	Pending     bool
	InstallCode int    // installation code; set only while Pending
	IDGroup     string // group id, for moving the agent between groups
}

// Supports reports whether the machine offers the given app (e.g. "shell",
// "filesystem"). Shares report an empty app list under fullAccess = all apps.
func (m Machine) Supports(app string) bool {
	if len(m.Apps) == 0 {
		return true
	}
	for _, a := range m.Apps {
		if a == app {
			return true
		}
	}
	return false
}

// SupportsShell reports whether the machine offers the shell app.
func (m Machine) SupportsShell() bool { return m.Supports("shell") }

type dsResponse struct {
	Items  []dsItem `json:"items"`
	Status string   `json:"status"`
}

type dsItem struct {
	Name                  string `json:"name"`
	ID                    string `json:"id"`
	OsType                int    `json:"osType"`
	AgentOsType           int    `json:"agentOsType"`
	State                 string `json:"state"`
	TempCode              int    `json:"tempCode"` // installation code, only while state is "W"
	IDGroup               string `json:"idGroup"`
	SupportedApplications string `json:"supportedApplications"`
	Group                 string `json:"group"`
	// share-only
	ShareID       string `json:"_id"`
	IDAgent       string `json:"idAgent"`
	UserDisplay   string `json:"userDisplayName"`
	PermissionSet struct {
		FullAccess   bool     `json:"fullAccess"`
		Applications []string `json:"applications"`
	} `json:"permissions"`
}

// machineFromAgent maps one owned-agent record. State "N" is online; "W" is
// created but not yet installed, which callers show as pending rather than as
// an ordinary offline machine, since such an agent carries a code and has no
// reported OS yet.
func machineFromAgent(it dsItem) Machine {
	return Machine{
		Name:        it.Name,
		ID:          it.ID,
		OS:          OS(it.OsType),
		Online:      it.State == "N",
		Pending:     it.State == "W",
		InstallCode: it.TempCode,
		Group:       it.Group,
		IDGroup:     it.IDGroup,
		Apps:        splitApps(it.SupportedApplications),
	}
}

// List returns owned agents followed by incoming shares.
func List(ctx context.Context, s *session.Session) ([]Machine, error) {
	var machines []Machine

	agentsRaw, err := s.Execute(ctx, "agent", "datasource", map[string]string{"operation": "load"})
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	var agents dsResponse
	if err := json.Unmarshal(agentsRaw, &agents); err != nil {
		return nil, fmt.Errorf("parse agents: %w", err)
	}
	for _, it := range agents.Items {
		machines = append(machines, machineFromAgent(it))
	}

	sharesRaw, err := s.Execute(ctx, "share", "datasource", map[string]string{
		"operation": "load", "name": "incoming",
	})
	if err == nil { // shares are optional; ignore if unavailable
		var shares dsResponse
		if json.Unmarshal(sharesRaw, &shares) == nil {
			for _, it := range shares.Items {
				machines = append(machines, Machine{
					Name:    it.Name,
					ID:      it.IDAgent,
					ShareID: it.ShareID,
					OS:      OS(it.AgentOsType),
					Online:  it.State == "N",
					Shared:  true,
					Owner:   it.UserDisplay,
					Group:   it.Group,
					Apps:    appsFromShare(it),
				})
			}
		}
	}
	return machines, nil
}

func splitApps(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ";")
}

func appsFromShare(it dsItem) []string {
	if it.PermissionSet.FullAccess {
		return nil // empty = all apps allowed
	}
	return it.PermissionSet.Applications
}

// ErrAmbiguous is returned when a name matches more than one machine.
type ErrAmbiguous struct{ Name string }

func (e *ErrAmbiguous) Error() string {
	return fmt.Sprintf("%q is ambiguous; use the id or --own/--shared", e.Name)
}

// ErrNotFound is returned when no machine matches.
type ErrNotFound struct{ Query string }

func (e *ErrNotFound) Error() string { return fmt.Sprintf("no machine matches %q", e.Query) }

// Filter narrows resolution to owned-only or shared-only.
type Filter int

const (
	Any Filter = iota
	OwnedOnly
	SharedOnly
)

// Resolve finds a machine by exact id, then by exact name, honoring the filter.
func Resolve(machines []Machine, query string, f Filter) (*Machine, error) {
	pass := func(m Machine) bool {
		switch f {
		case OwnedOnly:
			return !m.Shared
		case SharedOnly:
			return m.Shared
		default:
			return true
		}
	}
	// Exact id (agent id or share idAgent) is unambiguous.
	for i := range machines {
		if pass(machines[i]) && machines[i].ID == query {
			return &machines[i], nil
		}
	}
	// Exact name.
	var hits []*Machine
	for i := range machines {
		if pass(machines[i]) && strings.EqualFold(machines[i].Name, query) {
			hits = append(hits, &machines[i])
		}
	}
	switch len(hits) {
	case 0:
		return nil, &ErrNotFound{Query: query}
	case 1:
		return hits[0], nil
	default:
		return nil, &ErrAmbiguous{Name: query}
	}
}

// Connect opens a session to the machine's agent, ready to load an app.
func Connect(ctx context.Context, account *session.Session, m *Machine, client *http.Client) (*session.Session, error) {
	signKey, err := auth.NewSignKey()
	if err != nil {
		return nil, err
	}
	keyParam, err := signKey.ConnectionParam()
	if err != nil {
		return nil, err
	}

	var raw []byte
	if m.Shared {
		raw, err = account.Execute(ctx, "share", "connection", map[string]string{
			"share":      m.ShareID + "@" + m.ID,
			"sessionKey": keyParam,
			"newresp":    "true",
		})
	} else {
		raw, err = account.Execute(ctx, "agent", "connection", map[string]string{
			"agent":      m.ID,
			"sessionKey": keyParam,
			"newresp":    "true",
		})
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", m.Name, err)
	}
	var resp struct {
		URL    string `json:"url"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse connection: %w", err)
	}
	if resp.Status != "ok" || resp.URL == "" {
		return nil, fmt.Errorf("connection to %s failed: %s", m.Name, string(raw))
	}

	agentSess := session.New(resp.URL, signKey, client)
	if err := agentSess.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize agent session: %w", err)
	}
	return agentSess, nil
}

// SortMachines orders machines: online first, then owned before shared, by name.
func SortMachines(ms []Machine) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].Online != ms[j].Online {
			return ms[i].Online
		}
		if ms[i].Shared != ms[j].Shared {
			return !ms[i].Shared
		}
		return strings.ToLower(ms[i].Name) < strings.ToLower(ms[j].Name)
	})
}
