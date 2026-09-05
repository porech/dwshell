package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/porech/dwshell/internal/client"
	"github.com/porech/dwshell/internal/manage"
	"github.com/porech/dwshell/internal/remote"
	"github.com/porech/dwshell/internal/session"
	"github.com/porech/dwshell/internal/term"
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

// installInstructions renders what to do with a fresh installation code.
//
// It gives the code and the page, and nothing else. Two things are deliberately
// absent: any line that downloads the installer, because the page is where the
// licence is accepted, and any mention of the installer's unattended mode,
// because the service refuses it — an install run with -silent answers "Silent
// installation forbidden. Please contact the support." Documenting it would
// send people down a path that does not work.
func installInstructions(code int) string {
	return fmt.Sprintf(`Installation code: %s

Download the agent on the target machine and enter this code when the installer
asks for it:
     %s
`, formatCode(code), downloadPage)
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
	case "code":
		return cmdAgentCode(ctx, args[1:])
	case "rm":
		return cmdAgentLifecycle(ctx, args[1:], "rm")
	case "reinstall":
		return cmdAgentLifecycle(ctx, args[1:], "reinstall")
	case "group":
		return cmdAgentGroup(ctx, args[1:])
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

// confirm gates a change that cannot be undone. With a terminal it asks; with
// none — a script, a CI job — it refuses unless the caller passed --yes, so
// automation cannot delete a machine or invalidate a code by accident.
func confirm(prompt string, assumeYes, interactive bool) error {
	if assumeYes {
		return nil
	}
	if !interactive {
		return fmt.Errorf("%s refusing without a terminal; pass --yes to proceed", prompt)
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	var answer string
	_, _ = fmt.Fscanln(os.Stdin, &answer)
	if answer != "y" && answer != "Y" {
		return fmt.Errorf("cancelled")
	}
	return nil
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

// agentCodeFor reports why an agent has no installation code to show, or nil
// when it does. A code exists only between creation and installation.
func agentCodeFor(m *remote.Machine) error {
	if m.Shared {
		return fmt.Errorf("%s is a share — someone else's agent — so it has no installation code here", m.Name)
	}
	if !m.Pending {
		return fmt.Errorf("%s is already installed; `dwshell agent reinstall %s` mints a new code", m.Name, m.Name)
	}
	return nil
}

// agentSession resolves an owned agent and hands back a session for acting on
// the account, which every subcommand below needs.
func agentSession(ctx context.Context, configPath, query string) (*remote.Machine, *client.Client, *session.Session, int) {
	c, err := client.New(configPath)
	if err != nil {
		return nil, nil, nil, fail("%v", err)
	}
	machines, err := c.List(ctx)
	if err != nil {
		return nil, nil, nil, fail("%v", err)
	}
	m, err := resolveOwnAgent(machines, query)
	if err != nil {
		return nil, nil, nil, fail("%v", err)
	}
	sess, err := c.Session(ctx)
	if err != nil {
		return nil, nil, nil, fail("%v", err)
	}
	return m, c, sess, 0
}

func cmdAgentCode(ctx context.Context, args []string) int {
	fs := newFlags("agent code")
	var configPath string
	asJSON := false
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	name, flagArgs := extractPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if name == "" {
		return fail("usage: dwshell agent code <agent> [--json]")
	}
	m, _, _, code := agentSession(ctx, configPath, name)
	if m == nil {
		return code
	}
	if err := agentCodeFor(m); err != nil {
		return fail("%v", err)
	}
	if asJSON {
		printAgentJSON(agentJSON{
			ID: m.ID, Name: m.Name, State: "W",
			InstallCode: formatCode(m.InstallCode), DownloadURL: downloadPage,
		})
		return 0
	}
	fmt.Print(installInstructions(m.InstallCode))
	return 0
}

// cmdAgentLifecycle serves rm and reinstall, which differ only in what they do
// and what they warn about: both are irreversible and both confirm first.
func cmdAgentLifecycle(ctx context.Context, args []string, verb string) int {
	fs := newFlags("agent " + verb)
	var configPath string
	assumeYes := false
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.BoolVar(&assumeYes, "yes", false, "do not ask for confirmation")
	name, flagArgs := extractPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if name == "" {
		return fail("usage: dwshell agent %s <agent> [--yes]", verb)
	}
	m, _, sess, code := agentSession(ctx, configPath, name)
	if m == nil {
		return code
	}

	prompt := fmt.Sprintf("delete agent %q from the account?", m.Name)
	if verb == "reinstall" {
		prompt = fmt.Sprintf("mint a new installation code for %q, invalidating the current one?", m.Name)
	}
	if err := confirm(prompt, assumeYes, term.IsTTY()); err != nil {
		return fail("%v", err)
	}

	if verb == "rm" {
		if err := manage.DeleteAgent(ctx, sess, m.ID); err != nil {
			return fail("delete agent %q: %v", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "agent %q deleted\n", m.Name)
		return 0
	}
	if err := manage.ReinstallAgent(ctx, sess, m.ID); err != nil {
		return fail("reinstall agent %q: %v", m.Name, err)
	}
	// The command acknowledges without echoing the record, so the new code is
	// read back from the listing.
	c, err := client.New(configPath)
	if err != nil {
		return fail("%v", err)
	}
	machines, err := c.List(ctx)
	if err != nil {
		return fail("%v", err)
	}
	fresh, err := resolveOwnAgent(machines, m.ID)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Print(installInstructions(fresh.InstallCode))
	return 0
}

func cmdAgentGroup(ctx context.Context, args []string) int {
	fs := newFlags("agent group")
	var configPath string
	none := false
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.BoolVar(&none, "none", false, "remove the agent from its group")
	name, flagArgs := extractPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	groupName := ""
	if !none {
		if fs.NArg() != 1 {
			return fail("usage: dwshell agent group <agent> <group>   (or --none)")
		}
		groupName = fs.Arg(0)
	}
	if name == "" {
		return fail("usage: dwshell agent group <agent> <group>   (or --none)")
	}

	m, _, sess, code := agentSession(ctx, configPath, name)
	if m == nil {
		return code
	}
	idGroup := ""
	if groupName != "" {
		groups, err := manage.ListGroups(ctx, sess)
		if err != nil {
			return fail("%v", err)
		}
		g, err := manage.ResolveGroup(groups, groupName)
		if err != nil {
			return fail("%v", err)
		}
		idGroup = g.ID
	}
	if err := manage.SetAgentGroup(ctx, sess, m.ID, idGroup); err != nil {
		return fail("set group for %q: %v", m.Name, err)
	}
	if idGroup == "" {
		fmt.Fprintf(os.Stderr, "agent %q removed from its group\n", m.Name)
	} else {
		fmt.Fprintf(os.Stderr, "agent %q moved to group %q\n", m.Name, groupName)
	}
	return 0
}
