package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/porech/dwshell/internal/app/files"
	"github.com/porech/dwshell/internal/app/shell"
	"github.com/porech/dwshell/internal/client"
	"github.com/porech/dwshell/internal/remote"
	"github.com/porech/dwshell/internal/session"
)

// parseRemote splits an "agent:path" endpoint. It splits on the first colon so
// remote Windows paths (agent:C:\dir) work; the agent must be non-empty. An empty
// path is allowed and means the remote root (see canonicalRemotePath).
func parseRemote(s string) (agent, rpath string, err error) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return "", "", fmt.Errorf("expected agent:path, got %q", s)
	}
	return s[:i], s[i+1:], nil
}

// isRemoteEndpoint reports whether s looks like an "agent:path" remote endpoint
// rather than a local path. A single-letter agent followed by a separator is
// treated as a local Windows drive (C:\dir), not a remote agent.
func isRemoteEndpoint(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return false
	}
	if i == 1 && len(s) > 2 && (s[2] == '\\' || s[2] == '/') {
		return false // e.g. C:\Users or C:/Users → local Windows path
	}
	return true
}

// canonicalRemotePath maps a user-supplied remote path onto the agent's
// namespace, where '/' is the root. Relative paths are anchored to root and an
// empty path means root, so a path never resolves against the agent's own
// working directory.
//
// On Windows '/' masks the drive namespace: '/' (or empty) lists the drives via
// the '$' root sentinel; '/C:/dir' and 'C:/dir' both address drive C (a leading
// slash before a drive is stripped, since the agent rejects it); and a bare drive
// letter is expanded to its root ('C:' -> 'C:/') so it is not taken as the agent's
// working directory. '\' and '/' are interchangeable on Windows. On *nix, '\' is a
// literal filename character and is left untouched.
func canonicalRemotePath(p string, os remote.OS) string {
	if os == remote.OSWindows {
		p = strings.ReplaceAll(p, "\\", "/")
		if p == "" || p == "/" {
			return "$" // root: list drives
		}
		p = strings.TrimPrefix(p, "/") // '/C:/dir' -> 'C:/dir'
		if isDriveLetter(p) {
			p += "/" // 'C:' -> 'C:/' (drive root, not the agent's cwd)
		}
		return p
	}
	// *nix: anchor everything to the filesystem root.
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// isDriveLetter reports whether p is a bare Windows drive reference like "C:".
func isDriveLetter(p string) bool {
	return len(p) == 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z'))
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
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")

	endpoint, rest := extractAgent(args)
	if endpoint == "" {
		return fail("usage: dwshell ls <agent>[:<path>]")
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	// The path is optional: "dwshell ls <agent>" (or "<agent>:") lists the root.
	agent, rpath := endpoint, ""
	if strings.ContainsRune(endpoint, ':') {
		var err error
		if agent, rpath, err = parseRemote(endpoint); err != nil {
			return fail("%v", err)
		}
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	m, sess, err := c.ConnectApp(ctx, agent, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	entries, err := files.List(ctx, sess, canonicalRemotePath(rpath, m.OS))
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
	var own, shared, recursive bool
	fs := newFlags("get")
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")
	fs.BoolVar(&recursive, "r", false, "download a directory recursively")

	pos, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fail("usage: dwshell get <agent>:<remote> [local]")
	}
	agent, rpath, err := parseRemote(pos[0])
	if err != nil {
		return fail("%v", err)
	}
	localArg := ""
	if len(pos) == 2 {
		localArg = pos[1]
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	m, sess, err := c.ConnectApp(ctx, agent, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	rpath = canonicalRemotePath(rpath, m.OS)

	if recursive {
		base := path.Base(strings.TrimRight(rpath, "/"))
		localRoot := localArg
		if localRoot == "" {
			localRoot = base
		} else if fi, err := os.Stat(localRoot); err == nil && fi.IsDir() {
			localRoot = filepath.Join(localRoot, base)
		}
		count, total, err := files.GetRecursive(ctx, sess, rpath, localRoot)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(os.Stderr, "downloaded %d files (%d bytes) → %s\n", count, total, localRoot)
		return 0
	}

	local := localArg
	if local == "" {
		local = path.Base(rpath) // rpath normalized to '/'
	} else if fi, err := os.Stat(local); err == nil && fi.IsDir() {
		local = filepath.Join(local, path.Base(rpath))
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

// --- sync ---

func cmdSync(ctx context.Context, args []string) int {
	var configPath string
	var own, shared, sizeOnly, dryRun, del, checksum bool
	fs := newFlags("sync")
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")
	fs.BoolVar(&sizeOnly, "size-only", false, "compare by size only (ignore mtime)")
	fs.BoolVar(&dryRun, "n", false, "dry run: show what would change, do nothing")
	fs.BoolVar(&del, "delete", false, "delete destination entries not in the source")
	fs.BoolVar(&checksum, "checksum", false, "compare by SHA-256 content hash")

	pos, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) != 2 {
		return fail("usage: dwshell sync <src> <dst>  (exactly one of src/dst is agent:path)")
	}
	src, dst := pos[0], pos[1]
	srcR, dstR := isRemoteEndpoint(src), isRemoteEndpoint(dst)
	if srcR == dstR {
		return fail("exactly one of source and destination must be an agent:path endpoint")
	}

	var dir files.Direction
	var endpoint, localRoot string
	if srcR {
		dir, endpoint, localRoot = files.Download, src, dst
	} else {
		dir, endpoint, localRoot = files.Upload, dst, src
	}
	agent, rpath, err := parseRemote(endpoint)
	if err != nil {
		return fail("%v", err)
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	m, sess, err := c.ConnectApp(ctx, agent, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	rpath = canonicalRemotePath(rpath, m.OS)

	cfg := files.SyncConfig{
		Direction:  dir,
		LocalRoot:  localRoot,
		RemoteRoot: rpath,
		SizeOnly:   sizeOnly,
		DryRun:     dryRun,
		Delete:     del,
		Log: func(action, p string) {
			prefix := ""
			if dryRun {
				prefix = "would "
			}
			fmt.Fprintf(os.Stderr, "%s%s %s\n", prefix, action, p)
		},
	}
	if dir == files.Upload && !sizeOnly && !checksum {
		if m.OS.IsUnix() {
			cfg.SetRemoteMTimes = remoteMTimeSetter(sess, m, agent)
		} else {
			fmt.Fprintln(os.Stderr, "note: cannot set mtimes on a Windows remote; comparing by size only")
		}
	}
	if checksum {
		if m.OS.IsUnix() {
			cfg.Checksum = true
			cfg.RemoteHashes = remoteHasher(sess, m)
		} else {
			fmt.Fprintln(os.Stderr, "note: --checksum needs sha256sum on the remote (unavailable on Windows); using size+mtime")
		}
	}

	st, err := files.Sync(ctx, sess, cfg)
	if err != nil {
		return fail("%v", err)
	}
	verb := "synced"
	if dryRun {
		verb = "would sync"
	}
	fmt.Fprintf(os.Stderr, "%s: %d copied, %d up-to-date, %d deleted, %d bytes\n",
		verb, st.Copied, st.Skipped, st.Deleted, st.Bytes)
	return 0
}

// remoteMTimeSetter returns a function that sets mtimes on *nix remote files via
// the shell (the filesystem app has no set-mtime command). Failures are warned
// and swallowed (best-effort), so the sync still succeeds.
func remoteMTimeSetter(sess *session.Session, m *remote.Machine, agent string) func(context.Context, map[string]time.Time) error {
	return func(ctx context.Context, times map[string]time.Time) error {
		var b strings.Builder
		flush := func() error {
			if b.Len() == 0 {
				return nil
			}
			res, err := shell.Run(ctx, sess, m.OS, b.String(), 60*time.Second, localUser(), interactivePassword(localUser(), m.Name))
			b.Reset()
			if err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("touch exited %d", res.ExitCode)
			}
			return nil
		}
		for p, t := range times {
			line := fmt.Sprintf("touch -c -d @%d -- '%s'; ", t.Unix(), strings.ReplaceAll(p, "'", `'\''`))
			if b.Len()+len(line) > 60000 {
				if err := flush(); err != nil {
					fmt.Fprintf(os.Stderr, "note: could not set remote mtimes via shell: %v\n", err)
					return nil
				}
			}
			b.WriteString(line)
		}
		if err := flush(); err != nil {
			fmt.Fprintf(os.Stderr, "note: could not set remote mtimes via shell: %v\n", err)
		}
		return nil
	}
}

// remoteHasher returns a function that computes SHA-256 hashes of remote files
// via the shell (`sha256sum`), for `sync --checksum` on *nix remotes.
func remoteHasher(sess *session.Session, m *remote.Machine) func(context.Context, []string) (map[string]string, error) {
	return func(ctx context.Context, paths []string) (map[string]string, error) {
		out := map[string]string{}
		var b strings.Builder
		run := func() error {
			if b.Len() == 0 {
				return nil
			}
			cmd := "sha256sum -- " + b.String()
			b.Reset()
			res, err := shell.Run(ctx, sess, m.OS, cmd, 300*time.Second, localUser(), interactivePassword(localUser(), m.Name))
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(res.Output), "\n") {
				line = strings.TrimRight(line, "\r")
				i := strings.IndexByte(line, ' ')
				if i <= 0 {
					continue
				}
				hash := line[:i]
				name := strings.TrimLeft(line[i:], " *")
				if name != "" {
					out[name] = hash
				}
			}
			return nil
		}
		for _, p := range paths {
			arg := "'" + strings.ReplaceAll(p, "'", `'\''`) + "' "
			if b.Len()+len(arg) > 60000 {
				if err := run(); err != nil {
					return nil, err
				}
			}
			b.WriteString(arg)
		}
		if err := run(); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// --- rm ---

func cmdRm(ctx context.Context, args []string) int {
	var configPath string
	var own, shared, recursive bool
	fs := newFlags("rm")
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")
	fs.BoolVar(&recursive, "r", false, "remove directories recursively")

	pos, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) < 1 {
		return fail("usage: dwshell rm <agent>:<path> [<agent>:<path>...]")
	}

	// All targets must be on the same agent.
	var agent string
	var rpaths []string
	for _, p := range pos {
		h, rp, err := parseRemote(p)
		if err != nil {
			return fail("%v", err)
		}
		if agent == "" {
			agent = h
		} else if h != agent {
			return fail("all paths must be on the same agent (%q vs %q)", agent, h)
		}
		rpaths = append(rpaths, rp)
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	m, sess, err := c.ConnectApp(ctx, agent, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}

	if recursive {
		rc := 0
		for _, rp := range rpaths {
			rp = canonicalRemotePath(rp, m.OS)
			if err := files.RemoveRecursive(ctx, sess, rp); err != nil {
				rc = fail("%v", err)
				continue
			}
			fmt.Fprintf(os.Stderr, "removed %s:%s (recursively)\n", agent, rp)
		}
		return rc
	}

	// Group names by their parent directory; one batched request removes them all.
	byDir := map[string][]string{}
	var order []string
	for _, rp := range rpaths {
		rp = canonicalRemotePath(rp, m.OS)
		dir := path.Dir(rp)
		if !strings.HasSuffix(dir, "/") {
			dir += "/"
		}
		if _, ok := byDir[dir]; !ok {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], path.Base(rp))
	}
	groups := make([]files.RemoveGroup, len(order))
	for i, dir := range order {
		groups[i] = files.RemoveGroup{Dir: dir, Names: byDir[dir]}
	}
	if err := files.RemoveMany(ctx, sess, groups); err != nil {
		return fail("%v", err)
	}
	for _, dir := range order {
		for _, n := range byDir[dir] {
			fmt.Fprintf(os.Stderr, "removed %s:%s%s\n", agent, dir, n)
		}
	}
	return 0
}

// --- put ---

func cmdPut(ctx context.Context, args []string) int {
	var configPath string
	var own, shared, recursive bool
	fs := newFlags("put")
	var account string
	fs.StringVar(&configPath, "config", "", "config path")
	fs.StringVar(&account, "account", "", "account to use when several are logged in")
	fs.BoolVar(&own, "own", false, "owned agents only")
	fs.BoolVar(&shared, "shared", false, "incoming shares only")
	fs.BoolVar(&recursive, "r", false, "upload a directory recursively")

	pos, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(pos) != 2 {
		return fail("usage: dwshell put <local> <agent>:<remote>")
	}
	local := pos[0]
	agent, rpath, err := parseRemote(pos[1])
	if err != nil {
		return fail("%v", err)
	}
	filter, err := filterFrom(own, shared)
	if err != nil {
		return fail("%v", err)
	}

	c, err := client.New(configPath, account)
	if err != nil {
		return fail("%v", err)
	}
	m, sess, err := c.ConnectApp(ctx, agent, filter, "filesystem")
	if err != nil {
		return fail("%v", err)
	}
	if err := files.Open(ctx, sess); err != nil {
		return fail("%v", err)
	}
	rpath = canonicalRemotePath(rpath, m.OS)

	if recursive {
		if local == "-" {
			return fail("cannot use -r with stdin")
		}
		count, total, err := files.PutRecursive(ctx, sess, local, rpath)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(os.Stderr, "uploaded %d files (%d bytes) → %s:%s\n", count, total, agent, rpath)
		return 0
	}

	// If the remote ends with '/', treat it as a directory and append the base.
	if strings.HasSuffix(rpath, "/") {
		if local == "-" {
			return fail("a remote file name is required when reading from stdin")
		}
		rpath += filepath.Base(local)
	}
	n, err := files.Put(ctx, sess, local, rpath)
	if err != nil {
		return fail("%v", err)
	}
	sz := "?"
	if n >= 0 {
		sz = fmt.Sprintf("%d bytes", n)
	}
	fmt.Fprintf(os.Stderr, "uploaded %s (%s) → %s:%s\n", local, sz, agent, rpath)
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
