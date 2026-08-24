package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/porech/dwshell/internal/app/files"
	"github.com/porech/dwshell/internal/client"
	"github.com/porech/dwshell/internal/remote"
)

// parseRemote splits a "host:path" endpoint. It splits on the first colon so
// remote Windows paths (host:C:\dir) work; the host must be non-empty.
func parseRemote(s string) (host, rpath string, err error) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("expected host:path, got %q", s)
	}
	return s[:i], s[i+1:], nil
}

func filterFrom(own, shared bool) (remote.Filter, error) {
	switch {
	case own && shared:
		return remote.Any, fmt.Errorf("--own and --shared are mutually exclusive")
	case own:
		return remote.OwnedOnly, nil
	case shared:
		return remote.SharedOnly, nil
	default:
		return remote.Any, nil
	}
}

// --- ls ---

func cmdLs(ctx context.Context, args []string) int {
	var configPath string
	var own, shared bool
	fs := newFlags("ls")
	fs.StringVar(&configPath, "config", "", "config path")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")

	endpoint, rest := extractHost(args)
	if endpoint == "" {
		return fail("usage: dwshell ls <host>:<path>")
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	host, rpath, err := parseRemote(endpoint)
	if err != nil {
		return fail("%v", err)
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath)
	if err != nil {
		return fail("%v", err)
	}
	_, sess, err := c.ConnectApp(ctx, host, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	entries, err := files.List(ctx, sess, rpath)
	if err != nil {
		return fail("%v", err)
	}
	for _, e := range entries {
		typ := "-"
		name := e.Name
		size := fmt.Sprintf("%d", e.Size)
		if e.IsDir {
			typ = "d"
			name += "/"
			size = "-"
		}
		mtime := ""
		if !e.ModTime.IsZero() {
			mtime = e.ModTime.Format("2006-01-02 15:04")
		}
		fmt.Printf("%s%-4s %12s  %-16s  %s\n", typ, e.Rights, size, mtime, name)
	}
	return 0
}

// --- get ---

func cmdGet(ctx context.Context, args []string) int {
	var configPath string
	var own, shared bool
	fs := newFlags("get")
	fs.StringVar(&configPath, "config", "", "config path")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")

	pos, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fail("usage: dwshell get <host>:<remote> [local]")
	}
	host, rpath, err := parseRemote(pos[0])
	if err != nil {
		return fail("%v", err)
	}
	local := ""
	if len(pos) == 2 {
		local = pos[1]
	}
	if local == "" {
		local = path.Base(rpath) // remote paths use '/'
	} else if fi, err := os.Stat(local); err == nil && fi.IsDir() {
		local = filepath.Join(local, path.Base(rpath))
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath)
	if err != nil {
		return fail("%v", err)
	}
	_, sess, err := c.ConnectApp(ctx, host, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	n, err := files.Get(ctx, sess, rpath, local)
	if err != nil {
		return fail("%v", err)
	}
	if local != "-" {
		fmt.Fprintf(os.Stderr, "downloaded %s (%d bytes) → %s\n", rpath, n, local)
	}
	return 0
}

// --- put ---

func cmdPut(ctx context.Context, args []string) int {
	var configPath string
	var own, shared bool
	fs := newFlags("put")
	fs.StringVar(&configPath, "config", "", "config path")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")

	pos, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) != 2 {
		return fail("usage: dwshell put <local> <host>:<remote>")
	}
	local := pos[0]
	host, rpath, err := parseRemote(pos[1])
	if err != nil {
		return fail("%v", err)
	}
	// If the remote ends with '/', treat it as a directory and append the base.
	if strings.HasSuffix(rpath, "/") {
		if local == "-" {
			return fail("a remote file name is required when reading from stdin")
		}
		rpath += filepath.Base(local)
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath)
	if err != nil {
		return fail("%v", err)
	}
	_, sess, err := c.ConnectApp(ctx, host, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	n, err := files.Put(ctx, sess, local, rpath)
	if err != nil {
		return fail("%v", err)
	}
	sz := "?"
	if n >= 0 {
		sz = fmt.Sprintf("%d bytes", n)
	}
	fmt.Fprintf(os.Stderr, "uploaded %s (%s) → %s:%s\n", local, sz, host, rpath)
	return 0
}

// splitPositional separates positional args from flags. Bool flags only (the
// file commands use --config/--own/--shared), so a flag never consumes the next
// token except --config.
func splitPositional(args []string) (pos, flagArgs []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if len(a) > 0 && a[0] == '-' && a != "-" {
			flagArgs = append(flagArgs, a)
			if trimDashes(a) == "config" && !containsEq(a) && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			i++
			continue
		}
		pos = append(pos, a)
		i++
	}
	return pos, flagArgs
}
