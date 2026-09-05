// Command dwshell opens a shell on a remote DWService machine from the terminal,
// SSH-style, for humans or automated agents. See docs/DESIGN.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"time"

	xterm "golang.org/x/term"

	"github.com/porech/dwshell/internal/app/shell"
	"github.com/porech/dwshell/internal/client"
	"github.com/porech/dwshell/internal/remote"
	"github.com/porech/dwshell/internal/session"
	"github.com/porech/dwshell/internal/term"
)

// newFlags builds a FlagSet that reports errors to stderr without os.Exit.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func trimDashes(s string) string {
	for len(s) > 0 && s[0] == '-' {
		s = s[1:]
	}
	if j := indexByte(s, '='); j >= 0 {
		s = s[:j]
	}
	return s
}

func containsEq(s string) bool { return indexByte(s, '=') >= 0 }

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

const usage = `dwshell — remote shell over DWService

Usage:
  dwshell login [--user U] [--no-trusted]     Authenticate and persist the session
  dwshell logout [--all]                      Forget stored credentials
  dwshell account list|default|rm             Manage accounts (only if you log in with several)
  dwshell list [--json]                       List machines (agents + shares)
  dwshell <agent> [flags]                     Open an interactive shell
  dwshell <agent> -c "command" [flags]        Run a command and exit
  dwshell shell <agent> [flags]               Explicit form (use if <agent> is
                                              named like a subcommand)
  dwshell ls <agent>[:<path>]                 List a remote directory (root if omitted)
  dwshell get [-r] <agent>:<remote> [local]   Download a file or directory
  dwshell put [-r] <local> <agent>:<remote>   Upload a file or directory
  dwshell rm [-r] <agent>:<path> [<path>...]  Remove remote file(s)/dir(s)
  dwshell sync [-n] [--delete] [--checksum] <src> <dst>   One-way sync

Agent flags:
  -c string        Run command non-interactively, capture output, exit
  --own            Resolve <agent> among owned agents only
  --shared         Resolve <agent> among incoming shares only
  --term string    TERM to send to a *nix remote (default: local $TERM)
  --no-term        Do not send a TERM to the remote
  --timeout dur    Command timeout for -c (default: none)

Global:
  --config path    Config file (default: XDG/AppData location)
  --account email  Account to use when several are logged in (or DWSHELL_ACCOUNT)
  --version        Print version and exit

Remote paths:
  Remote paths are always rooted at "/", so "/etc" and a relative "etc" mean the
  same thing. On Windows "/" is the root and lists the drives; address a drive as
  "/C:/dir" or "C:/dir" ('/' and '\' are interchangeable). If the path is omitted,
  the root is used.

Agent name vs subcommand:
  "dwshell <agent>" is a shortcut: the first argument is treated as an agent unless
  it is one of the subcommands listed above. If a machine is actually named like
  one of those, use the explicit form "dwshell shell <agent>", which always treats
  the argument as an agent (e.g. "dwshell shell version" → agent named "version").
`

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Flags may be written anywhere, so the command is the first positional
	// rather than simply os.Args[1]: `dwshell --account a@b list` and
	// `dwshell list --account a@b` are the same command. What follows it is
	// handed on with the surrounding flags reattached.
	globalFlags, pos := partitionArgs(os.Args[1:])
	command := ""
	if len(pos) > 0 {
		command = pos[0]
	}
	sub := func() []string {
		rest := append([]string{}, globalFlags...)
		return append(rest, pos[1:]...)
	}

	switch os.Args[1] {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "-V", "--version", "version":
		fmt.Println("dwshell " + versionString())
		return 0
	}

	switch command {
	case "help":
		fmt.Print(usage)
		return 0
	case "version":
		fmt.Println("dwshell " + versionString())
		return 0
	case "login":
		return cmdLogin(ctx, sub())
	case "account":
		return cmdAccount(ctx, sub())
	case "logout":
		return cmdLogout(ctx, sub())
	case "list":
		return cmdList(ctx, sub())
	case "ls":
		return cmdLs(ctx, sub())
	case "get":
		return cmdGet(ctx, sub())
	case "put":
		return cmdPut(ctx, sub())
	case "rm":
		return cmdRm(ctx, sub())
	case "sync":
		return cmdSync(ctx, sub())
	case "shell":
		// Explicit form: the next argument is always an agent, even if it happens
		// to be named like a subcommand (e.g. `dwshell shell version`).
		return cmdAgent(ctx, sub())
	default:
		// Shortcut form: `dwshell <agent> [flags]`. If your agent is named like a
		// subcommand, use the explicit `dwshell shell <agent>` form above.
		return cmdAgent(ctx, os.Args[1:])
	}
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "dwshell: "+format+"\n", a...)
	return 1
}

// --- login ---

func cmdLogin(ctx context.Context, args []string) int {
	var user, configPath string
	noTrusted := false
	fs := newFlags("login")
	var account string
	fs.StringVar(&user, "user", "", "account user (email)")
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&noTrusted, "no-trusted", false, "do not register a trusted device")
	flagArgs, _ := partitionArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}

	if account != "" {
		return fail("--account does not apply to login: which account it touches is decided by the email you log in with")
	}
	c, err := client.NewForLogin(configPath)
	if err != nil {
		return fail("%v", err)
	}
	if user == "" {
		// Re-logging in with no --user refreshes the default account.
		user = c.Config().Default
	}
	if user == "" {
		fmt.Fprint(os.Stderr, "User (email): ")
		fmt.Scanln(&user)
	}
	if user == "" {
		return fail("a user is required")
	}
	password, err := readPassword()
	if err != nil {
		return fail("%v", err)
	}

	if err := c.Login(ctx, user, password, !noTrusted, twoFactorProvider()); err != nil {
		return fail("login failed: %v", err)
	}
	msg := "logged in; session saved"
	if !noTrusted {
		msg += " (trusted device registered)"
	}
	fmt.Fprintln(os.Stderr, msg+" to "+c.Config().Path())
	return 0
}

func readPassword() (string, error) {
	if p := os.Getenv("DWSHELL_PASSWORD"); p != "" {
		return p, nil
	}
	if !xterm.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("no TTY for password prompt; set DWSHELL_PASSWORD")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	b, err := xterm.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// --- logout ---

func cmdLogout(ctx context.Context, args []string) int {
	var configPath string
	all := false
	fs := newFlags("logout")
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&all, "all", false, "forget every account")
	flagArgs, _ := partitionArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if all {
		// --all must work even when several accounts are configured with no
		// default, which is precisely a state someone might want to clear.
		c, err := client.NewForLogin(configPath)
		if err != nil {
			return fail("%v", err)
		}
		if err := c.LogoutAll(ctx); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintln(os.Stderr, "logged out of every account")
		return 0
	}
	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	if err := c.Logout(ctx); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintln(os.Stderr, "logged out")
	return 0
}

// --- list ---

func cmdList(ctx context.Context, args []string) int {
	var configPath string
	asJSON := false
	fs := newFlags("list")
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&asJSON, "json", false, "JSON output")
	flagArgs, _ := partitionArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	machines, err := c.List(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(machines)
		return 0
	}
	for _, m := range machines {
		state := "offline"
		if m.Online {
			state = "online"
		}
		kind := "own"
		if m.Shared {
			kind = "shared"
		}
		fmt.Printf("%-24s %-8s %-7s %-7s %s\n", m.Name, m.OS, state, kind, m.ID)
	}
	return 0
}

// --- agent (interactive / -c) ---

func cmdAgent(ctx context.Context, args []string) int {
	// args[0] is the agent; flags may precede or follow it.
	var command, termValue, configPath string
	var own, shared, noTerm bool
	var timeout time.Duration
	fs := newFlags("agent")
	var account string
	fs.StringVar(&command, "c", "", "run command and exit")
	fs.StringVar(&termValue, "term", "", "TERM for *nix remote")
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")
	fs.BoolVar(&noTerm, "no-term", false, "do not send TERM")
	fs.DurationVar(&timeout, "timeout", 0, "command timeout for -c (0 = no timeout)")

	rest, pos := partitionArgs(args)
	agentArg := ""
	if len(pos) > 0 {
		agentArg = pos[0]
	}
	if agentArg == "" {
		return fail("an agent is required (see `dwshell --help`)")
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	username, agent, userExplicit := parseUserAndAgent(agentArg)

	filter := remote.Any
	switch {
	case own && shared:
		return fail("--own and --shared are mutually exclusive")
	case own:
		filter = remote.OwnedOnly
	case shared:
		filter = remote.SharedOnly
	}

	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	m, sess, err := c.Connect(ctx, agent, filter)
	if err != nil {
		return fail("%v", err)
	}

	if command != "" {
		res, err := shell.Run(ctx, sess, m.OS, command, timeout, username, nonInteractivePassword(username, agent))
		if err != nil {
			return fail("%v", err)
		}
		if userExplicit && !res.Authenticated {
			warnUserIgnored(username, m.Name)
		}
		os.Stdout.Write(res.Output)
		if len(res.Output) > 0 {
			fmt.Println()
		}
		return res.ExitCode
	}

	return interactive(ctx, sess, m, username, userExplicit, termValue, noTerm)
}

// parseUserAndAgent splits "user@agent" into (user, agent, explicit); when no user is
// given the local username is used, SSH-style, and explicit is false.
func parseUserAndAgent(arg string) (user, agent string, explicit bool) {
	if i := strings.LastIndexByte(arg, '@'); i >= 0 {
		return arg[:i], arg[i+1:], true
	}
	return localUser(), arg, false
}

func warnUserIgnored(username, agent string) {
	fmt.Fprintf(os.Stderr, "dwshell: note: %s does not require authentication; the user %q is ignored (shell runs as the agent's user)\n", agent, username)
}

func localUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "root"
}

// interactivePassword prompts on the TTY (agent asked for a password).
func interactivePassword(user, agent string) shell.PasswordFunc {
	return func(u string, retry bool) (string, error) {
		if p := os.Getenv("DWSHELL_REMOTE_PASSWORD"); p != "" && !retry {
			return p, nil
		}
		if !xterm.IsTerminal(int(os.Stdin.Fd())) {
			return "", fmt.Errorf("agent requires a password for %s@%s; set DWSHELL_REMOTE_PASSWORD", u, agent)
		}
		label := fmt.Sprintf("%s@%s's password: ", u, agent)
		if retry {
			label = "Sorry, try again.\n" + label
		}
		fmt.Fprint(os.Stderr, label)
		b, err := xterm.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
}

// nonInteractivePassword supplies the remote password for -c from the
// environment (or a flag); it never prompts.
func nonInteractivePassword(user, agent string) shell.PasswordFunc {
	return func(u string, retry bool) (string, error) {
		if p := os.Getenv("DWSHELL_REMOTE_PASSWORD"); p != "" && !retry {
			return p, nil
		}
		return "", fmt.Errorf("agent requires a password for %s@%s; set DWSHELL_REMOTE_PASSWORD", u, agent)
	}
}

func interactive(ctx context.Context, sess *session.Session, m *remote.Machine, username string, userExplicit bool, termValue string, noTerm bool) int {
	if m.OS == remote.OSWindows {
		// Print the disconnect hint before the shell connects: a Windows agent
		// clears the screen on start, which cleanly resyncs the terminal. Printing
		// the hint after the prompt is drawn instead would desync the local cursor
		// from the remote PTY and corrupt the display.
		fmt.Fprintln(os.Stderr, "dwshell: type ~. on a fresh line to disconnect (a Windows shell won't report 'exit').")
	}
	cols, rows := term.Size()
	sh, err := shell.Open(ctx, sess, cols, rows)
	if err != nil {
		return fail("%v", err)
	}
	defer sh.Close()

	// Handle agent-side login if required, then display any initial output.
	pre, authed, err := sh.Authenticate(ctx, username, interactivePassword(username, m.Name), 3)
	if err != nil {
		return fail("%v", err)
	}
	if userExplicit && !authed {
		warnUserIgnored(username, m.Name)
	}
	if len(pre) > 0 {
		os.Stdout.Write(pre)
	}

	if !noTerm && m.OS.IsUnix() {
		tv := termValue
		if tv == "" {
			tv = os.Getenv("TERM")
		}
		if tv == "" {
			tv = "xterm-256color"
		}
		// Set TERM (SSH-like). We don't clear the screen — the export command is
		// left visible, like a login shell echoing its startup.
		_ = sh.Input("export TERM=" + tv + "\r")
	}

	if err := term.Bridge(ctx, sh); err != nil && !isInterrupt(err) {
		return fail("%v", err)
	}
	if e := sh.Err(); e != nil {
		return fail("connection lost: %v", e)
	}
	return 0
}

func isInterrupt(err error) bool { return err == context.Canceled }
