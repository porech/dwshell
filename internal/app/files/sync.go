package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/porech/dwshell/internal/session"
)

// Direction of a one-way sync.
type Direction int

const (
	Upload   Direction = iota // local → remote
	Download                  // remote → local
)

// mtimeTolerance absorbs filesystem timestamp granularity differences.
const mtimeTolerance = 2 * time.Second

// SyncConfig configures a one-way sync that makes the destination match the
// source, transferring only files that differ by size or mtime.
type SyncConfig struct {
	Direction  Direction
	LocalRoot  string
	RemoteRoot string
	SizeOnly   bool
	DryRun     bool
	Delete     bool // remove destination entries not present in the source

	// SetRemoteMTimes, if non-nil, sets mtimes on remote files after an upload
	// (the filesystem app has no set-mtime command, so this is done via the
	// shell). times maps remote full path → desired mtime. When nil, uploads
	// fall back to size-only comparison to avoid re-copying every run.
	SetRemoteMTimes func(ctx context.Context, times map[string]time.Time) error

	// Log receives one line per planned/performed action (may be nil).
	Log func(action, path string)
}

// SyncStats summarizes a sync run.
type SyncStats struct {
	Copied  int
	Skipped int
	Deleted int
	Bytes   int64
}

type localFile struct {
	rel     string
	size    int64
	modTime time.Time
}

// Sync performs a one-way sync per cfg.
func Sync(ctx context.Context, sess *session.Session, cfg SyncConfig) (SyncStats, error) {
	sizeOnly := cfg.SizeOnly
	if cfg.Direction == Upload && cfg.SetRemoteMTimes == nil {
		sizeOnly = true // can't set remote mtime → mtime compare would re-copy forever
	}

	// Index the remote tree.
	rfiles, rdirs, err := Walk(ctx, sess, cfg.RemoteRoot)
	if err != nil {
		// A missing remote root is fine for uploads (it will be created).
		if cfg.Direction != Upload {
			return SyncStats{}, err
		}
	}
	remoteRoot := strings.TrimRight(cfg.RemoteRoot, "/")
	remoteIndex := map[string]Entry{}
	for _, f := range rfiles {
		remoteIndex[remoteRel(remoteRoot, f.Path)] = f.Entry
	}
	remoteDirs := map[string]bool{}
	for _, d := range rdirs {
		remoteDirs[remoteRel(remoteRoot, d.Path)] = true
	}

	// Index the local tree.
	localIndex := map[string]localFile{}
	localDirs := map[string]bool{}
	if fi, err := os.Stat(cfg.LocalRoot); err == nil && fi.IsDir() {
		err = filepath.WalkDir(cfg.LocalRoot, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(cfg.LocalRoot, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return nil
			}
			if d.IsDir() {
				localDirs[rel] = true
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			localIndex[rel] = localFile{rel: rel, size: info.Size(), modTime: info.ModTime()}
			return nil
		})
		if err != nil && cfg.Direction == Upload {
			return SyncStats{}, err
		}
	} else if cfg.Direction == Upload {
		return SyncStats{}, fmt.Errorf("local source %q is not a directory", cfg.LocalRoot)
	}

	if cfg.Direction == Upload {
		return syncUpload(ctx, sess, cfg, sizeOnly, localIndex, localDirs, remoteIndex, remoteDirs)
	}
	return syncDownload(ctx, sess, cfg, sizeOnly, remoteIndex, remoteDirs, localIndex, localDirs)
}

func remoteRel(root, full string) string {
	rel := strings.TrimPrefix(full, root)
	return strings.TrimPrefix(rel, "/")
}

func (c SyncConfig) log(action, path string) {
	if c.Log != nil {
		c.Log(action, path)
	}
}

func needsCopy(srcSize int64, srcMod time.Time, dst Entry, dstOK, sizeOnly bool) bool {
	return needsCopyTimes(srcSize, srcMod, dst.Size, dst.ModTime, dstOK, sizeOnly)
}

func needsCopyTimes(srcSize int64, srcMod time.Time, dstSize int64, dstMod time.Time, dstOK, sizeOnly bool) bool {
	if !dstOK {
		return true
	}
	if srcSize != dstSize {
		return true
	}
	if sizeOnly {
		return false
	}
	diff := srcMod.Sub(dstMod)
	if diff < 0 {
		diff = -diff
	}
	return diff > mtimeTolerance
}

func syncUpload(ctx context.Context, sess *session.Session, cfg SyncConfig, sizeOnly bool,
	local map[string]localFile, localDirs map[string]bool, remote map[string]Entry, remoteDirs map[string]bool) (SyncStats, error) {
	var st SyncStats
	remoteRoot := strings.TrimRight(cfg.RemoteRoot, "/")

	// Ensure destination directories exist (root first, then children).
	if !cfg.DryRun {
		if err := Mkdir(ctx, sess, remoteRoot); err != nil {
			return st, err
		}
		for _, rel := range sortedKeys(localDirs) {
			if err := Mkdir(ctx, sess, remoteRoot+"/"+rel); err != nil {
				return st, err
			}
		}
	}

	setTimes := map[string]time.Time{}
	for _, rel := range sortedKeys(local) {
		lf := local[rel]
		dst, ok := remote[rel]
		if !needsCopy(lf.size, lf.modTime, dst, ok, sizeOnly) {
			st.Skipped++
			continue
		}
		full := remoteRoot + "/" + rel
		cfg.log("upload", rel)
		if cfg.DryRun {
			st.Copied++
			continue
		}
		f, err := os.Open(filepath.Join(cfg.LocalRoot, filepath.FromSlash(rel)))
		if err != nil {
			return st, err
		}
		err = sess.Upload(ctx, module, full, f)
		f.Close()
		if err != nil {
			return st, fmt.Errorf("upload %s: %w", rel, err)
		}
		setTimes[full] = lf.modTime
		st.Copied++
		st.Bytes += lf.size
	}

	if !cfg.DryRun && cfg.SetRemoteMTimes != nil && len(setTimes) > 0 {
		if err := cfg.SetRemoteMTimes(ctx, setTimes); err != nil {
			return st, fmt.Errorf("set remote mtimes: %w", err)
		}
	}

	if cfg.Delete {
		var extra []string
		for rel := range remote {
			if _, ok := local[rel]; !ok {
				extra = append(extra, remoteRoot+"/"+rel)
			}
		}
		for rel := range remoteDirs {
			if !localDirs[rel] {
				extra = append(extra, remoteRoot+"/"+rel)
			}
		}
		for _, full := range extra {
			cfg.log("delete", remoteRel(remoteRoot, full))
		}
		if !cfg.DryRun && len(extra) > 0 {
			if err := deleteRemotePaths(ctx, sess, extra); err != nil {
				return st, err
			}
		}
		st.Deleted = len(extra)
	}
	return st, nil
}

func syncDownload(ctx context.Context, sess *session.Session, cfg SyncConfig, sizeOnly bool,
	remote map[string]Entry, remoteDirs map[string]bool, local map[string]localFile, localDirs map[string]bool) (SyncStats, error) {
	var st SyncStats

	if !cfg.DryRun {
		if err := os.MkdirAll(cfg.LocalRoot, 0o755); err != nil {
			return st, err
		}
		for _, rel := range sortedKeys(remoteDirs) {
			if err := os.MkdirAll(filepath.Join(cfg.LocalRoot, filepath.FromSlash(rel)), 0o755); err != nil {
				return st, err
			}
		}
	}

	remoteRoot := strings.TrimRight(cfg.RemoteRoot, "/")
	for _, rel := range sortedKeys(remote) {
		re := remote[rel]
		lf, ok := local[rel]
		if !needsCopyTimes(re.Size, re.ModTime, lf.size, lf.modTime, ok, sizeOnly) {
			st.Skipped++
			continue
		}
		cfg.log("download", rel)
		if cfg.DryRun {
			st.Copied++
			continue
		}
		lp := filepath.Join(cfg.LocalRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
			return st, err
		}
		n, err := Get(ctx, sess, remoteRoot+"/"+rel, lp)
		if err != nil {
			return st, fmt.Errorf("download %s: %w", rel, err)
		}
		if !re.ModTime.IsZero() {
			_ = os.Chtimes(lp, re.ModTime, re.ModTime)
		}
		st.Copied++
		st.Bytes += n
	}

	if cfg.Delete {
		var extraFiles, extraDirs []string
		for rel := range local {
			if _, ok := remote[rel]; !ok {
				extraFiles = append(extraFiles, rel)
			}
		}
		for rel := range localDirs {
			if !remoteDirs[rel] {
				extraDirs = append(extraDirs, rel)
			}
		}
		for _, rel := range append(append([]string{}, extraFiles...), extraDirs...) {
			cfg.log("delete", rel)
		}
		st.Deleted = len(extraFiles) + len(extraDirs)
		if !cfg.DryRun {
			for _, rel := range extraFiles {
				if err := os.Remove(filepath.Join(cfg.LocalRoot, filepath.FromSlash(rel))); err != nil {
					return st, err
				}
			}
			// Remove now-empty extraneous dirs deepest-first.
			sort.Slice(extraDirs, func(i, j int) bool {
				return strings.Count(extraDirs[i], "/") > strings.Count(extraDirs[j], "/")
			})
			for _, rel := range extraDirs {
				_ = os.Remove(filepath.Join(cfg.LocalRoot, filepath.FromSlash(rel)))
			}
		}
	}
	return st, nil
}

// deleteRemotePaths removes remote full paths deepest-first, batched per depth.
func deleteRemotePaths(ctx context.Context, sess *session.Session, fulls []string) error {
	sort.Slice(fulls, func(i, j int) bool {
		return strings.Count(fulls[i], "/") > strings.Count(fulls[j], "/")
	})
	i := 0
	for i < len(fulls) {
		depth := strings.Count(fulls[i], "/")
		byDir := map[string][]string{}
		var order []string
		for i < len(fulls) && strings.Count(fulls[i], "/") == depth {
			p := fulls[i]
			dir := p[:strings.LastIndexByte(p, '/')+1]
			base := p[strings.LastIndexByte(p, '/')+1:]
			if _, ok := byDir[dir]; !ok {
				order = append(order, dir)
			}
			byDir[dir] = append(byDir[dir], base)
			i++
		}
		groups := make([]RemoveGroup, len(order))
		for j, dir := range order {
			groups[j] = RemoveGroup{Dir: dir, Names: byDir[dir]}
		}
		if err := RemoveMany(ctx, sess, groups); err != nil {
			return err
		}
	}
	return nil
}

// sortedKeys returns the map keys sorted; lexical order places parent paths
// before their children (e.g. "a" before "a/b").
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
