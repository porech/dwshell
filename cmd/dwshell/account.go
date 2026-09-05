package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/porech/dwshell/internal/client"
	"github.com/porech/dwshell/internal/config"
	"github.com/porech/dwshell/internal/term"
)

// cmdAccount dispatches `dwshell account <verb>`, which exists only for people
// who log in with more than one account. With a single account there is nothing
// here worth running.
func cmdAccount(ctx context.Context, args []string) int {
	// The verb is the first positional, so flags may sit on either side of it.
	flagArgs, pos := partitionArgs(args)
	if len(pos) == 0 {
		return fail("usage: dwshell account <list|default|rm>")
	}
	rest := append(append([]string{}, flagArgs...), pos[1:]...)
	switch pos[0] {
	case "list":
		return cmdAccountList(rest)
	case "default":
		return cmdAccountDefault(rest)
	case "rm":
		return cmdAccountRemove(ctx, rest)
	default:
		return fail("unknown account subcommand %q", pos[0])
	}
}

// accountJSON is the --json shape of one registered account.
type accountJSON struct {
	User    string `json:"user"`
	Default bool   `json:"default"`
}

func cmdAccountList(args []string) int {
	fs := newFlags("account list")
	var configPath string
	asJSON := false
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.BoolVar(&asJSON, "json", false, "machine-readable output")
	flagArgs, _ := partitionArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fail("%v", err)
	}
	if len(cfg.Accounts) == 0 {
		return fail("%v", config.ErrNoAccounts)
	}
	if asJSON {
		out := make([]accountJSON, 0, len(cfg.Accounts))
		for _, a := range cfg.Accounts {
			out = append(out, accountJSON{User: a.User, Default: a.User == cfg.Default})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	for _, a := range cfg.Accounts {
		name := a.User
		if name == "" {
			name = "(unnamed)"
		}
		if a.User == cfg.Default {
			fmt.Printf("%s  (default)\n", name)
			continue
		}
		fmt.Println(name)
	}
	return 0
}

func cmdAccountDefault(args []string) int {
	fs := newFlags("account default")
	var configPath string
	fs.StringVar(&configPath, "config", "", "config file path")
	flagArgs, pos := partitionArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) != 1 {
		return fail("usage: dwshell account default <email>")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fail("%v", err)
	}
	if err := cfg.SetDefault(pos[0]); err != nil {
		return fail("%v", err)
	}
	if err := cfg.Save(); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(os.Stderr, "default account is now %s\n", pos[0])
	return 0
}

func cmdAccountRemove(ctx context.Context, args []string) int {
	fs := newFlags("account rm")
	var configPath string
	assumeYes := false
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.BoolVar(&assumeYes, "yes", false, "do not ask for confirmation")
	flagArgs, pos := partitionArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	email := ""
	if len(pos) > 0 {
		email = pos[0]
	}
	if email == "" {
		return fail("usage: dwshell account rm <email> [--yes]")
	}
	if err := confirm(fmt.Sprintf("remove account %q and deregister its trusted device?", email),
		assumeYes, term.IsTTY()); err != nil {
		return fail("%v", err)
	}
	// Selecting the account being removed is what lets the client deregister its
	// trusted device server-side before forgetting it locally.
	c, err := client.New(configPath, email)
	if err != nil {
		return fail("%v", err)
	}
	if err := c.Logout(ctx); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(os.Stderr, "account %s removed\n", email)
	return 0
}

// confirm gates a change that cannot be undone. With a terminal it asks; with
// none — a script, a CI job — it refuses unless the caller passed --yes, so
// automation cannot drop an account by accident.
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
