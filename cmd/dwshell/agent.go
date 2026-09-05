package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/porech/dwshell/internal/client"
	"github.com/porech/dwshell/internal/manage"
	"github.com/porech/dwshell/internal/remote"
)

// downloadPage is where the DWService agent is obtained. It is deliberately the
// page and not the installer file: the page carries the licence acceptance
// ("By selecting the 'Download' button I accept the Terms and Conditions and
// the Restrictive Terms and Conditions"), and a download-and-run one-liner —
// convenient as it would be — would route around that. The direct file URLs are
// known and stay unused.
const downloadPage = "https://www.dwservice.net/download.html"

// formatCode renders an installation code the way the web client shows it and
// the way the installer expects it: three groups of three, dash-separated. The
// code arrives as a JSON number, so it is padded back to nine digits first — a
// leading zero could not have survived the wire.
func formatCode(code int) string {
	s := fmt.Sprintf("%09d", code)
	return s[0:3] + "-" + s[3:6] + "-" + s[6:]
}

// installInstructions renders what to do with a fresh installation code: fetch
// the agent by hand from the download page, then run the silent install there.
func installInstructions(code int) string {
	c := formatCode(code)
	return fmt.Sprintf(`Installation code: %s

1. Download the agent on the target machine (this is where you accept the licence):
     %s

2. Run the unattended setup there:
     Linux / macOS   sudo sh dwagent.sh -silent key=%s
     Windows         dwagent.exe -silent key=%s
`, c, downloadPage, c, c)
}

// agentJSON is the --json shape of an agent and, when it has one, its code.
type agentJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	InstallCode string `json:"installCode,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty"`
}

func printAgentJSON(a agentJSON) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(a)
}

// agentValueFlags are the `agent` subcommand flags that consume a following
// value token, so a positional can be pulled out from among them.
var agentValueFlags = map[string]bool{"config": true, "description": true, "group": true}

// extractPositional pulls the first non-flag token out of a subcommand's args,
// returning it with the flags that surrounded it. Go's flag package stops at
// the first positional, so `agent create NAME --json` would otherwise silently
// drop --json; this mirrors what extractAgent does for the shell shortcut.
func extractPositional(args []string) (pos string, flagArgs []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if len(a) > 0 && a[0] == '-' {
			flagArgs = append(flagArgs, a)
			if !containsEq(a) && agentValueFlags[trimDashes(a)] && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			i++
			continue
		}
		return a, append(flagArgs, args[i+1:]...)
	}
	return "", flagArgs
}

// cmdAgentManage dispatches the `dwshell agent <verb>` family. It is separate
// from cmdAgent, which is the shell shortcut for `dwshell <agent>`.
func cmdAgentManage(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return fail("usage: dwshell agent <create|code|reinstall|rm|group> …")
	}
	switch args[0] {
	case "create":
		return cmdAgentCreate(ctx, args[1:])
	default:
		return fail("unknown agent subcommand %q", args[0])
	}
}

func cmdAgentCreate(ctx context.Context, args []string) int {
	fs := newFlags("agent create")
	var configPath, description, group string
	asJSON := false
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.StringVar(&description, "description", "", "free-text description")
	fs.StringVar(&group, "group", "", "existing group to place the agent in")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	name, flagArgs := extractPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if name == "" || fs.NArg() != 0 {
		return fail("usage: dwshell agent create <name> [--group G] [--description D] [--json]")
	}

	c, err := client.New(configPath)
	if err != nil {
		return fail("%v", err)
	}
	sess, err := c.Session(ctx)
	if err != nil {
		return fail("%v", err)
	}

	idGroup := ""
	if group != "" {
		groups, err := manage.ListGroups(ctx, sess)
		if err != nil {
			return fail("%v", err)
		}
		g, err := manage.ResolveGroup(groups, group)
		if err != nil {
			return fail("%v", err)
		}
		idGroup = g.ID
	}

	a, err := manage.CreateAgent(ctx, sess, name, description, idGroup)
	if err != nil {
		return fail("create agent %q: %v", name, err)
	}
	if asJSON {
		printAgentJSON(agentJSON{
			ID: a.ID, Name: a.Name, State: a.State,
			InstallCode: formatCode(a.InstallCode), DownloadURL: downloadPage,
		})
		return 0
	}
	fmt.Printf("Agent %q created.\n\n%s", a.Name, installInstructions(a.InstallCode))
	return 0
}

// resolveOwnAgent finds an agent by name or id and refuses anything these
// commands cannot act on: a share is someone else's agent.
func resolveOwnAgent(machines []remote.Machine, query string) (*remote.Machine, error) {
	m, err := remote.Resolve(machines, query, remote.Any)
	if err != nil {
		return nil, err
	}
	if m.Shared {
		return nil, fmt.Errorf("%s is a share — someone else's agent — so it cannot be managed from this account", m.Name)
	}
	return m, nil
}
