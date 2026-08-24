package files

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/porech/dwshell/internal/session"
)

// walkBatch caps how many directories are listed per batched request.
const walkBatch = 64

// Node is an entry discovered by Walk, with its full remote path.
type Node struct {
	Path  string // full remote path, no trailing slash
	Entry Entry
}

// Walk recursively lists a remote directory tree rooted at root, breadth-first,
// batching each level's directory listings into few requests. It returns the
// files and directories found (not including root itself).
func Walk(ctx context.Context, sess *session.Session, root string) (files, dirs []Node, err error) {
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}
	queue := []string{root}
	for len(queue) > 0 {
		n := min(len(queue), walkBatch)
		batch := queue[:n]
		queue = queue[n:]

		cmds := make([]session.Command, len(batch))
		for i, d := range batch {
			cmds[i] = session.Command{Module: module, Command: "list", Params: map[string]string{"path": d}}
		}
		results, err := sess.ExecuteBatch(ctx, cmds)
		if err != nil {
			return nil, nil, err
		}
		for i, d := range batch {
			if results[i].Err != nil {
				return nil, nil, fmt.Errorf("list %s: %w", d, results[i].Err)
			}
			entries, err := parseList(results[i].Data)
			if err != nil {
				return nil, nil, err
			}
			for _, e := range entries {
				full := joinRemote(d, e.Name)
				if e.IsDir {
					dirs = append(dirs, Node{Path: full, Entry: e})
					queue = append(queue, full)
				} else {
					files = append(files, Node{Path: full, Entry: e})
				}
			}
		}
	}
	return files, dirs, nil
}

func joinRemote(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// GetRecursive downloads the remote tree at remoteRoot into localRoot (which is
// created). Returns the number of files and total bytes transferred.
func GetRecursive(ctx context.Context, sess *session.Session, remoteRoot, localRoot string) (int, int64, error) {
	remoteRoot = strings.TrimRight(remoteRoot, "/")
	files, dirs, err := Walk(ctx, sess, remoteRoot)
	if err != nil {
		return 0, 0, err
	}
	localOf := func(remotePath string) string {
		rel := strings.TrimPrefix(remotePath, remoteRoot)
		rel = strings.TrimPrefix(rel, "/")
		return filepath.Join(localRoot, filepath.FromSlash(rel))
	}
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return 0, 0, err
	}
	for _, d := range dirs {
		if err := os.MkdirAll(localOf(d.Path), 0o755); err != nil {
			return 0, 0, err
		}
	}
	var count int
	var total int64
	for _, f := range files {
		lp := localOf(f.Path)
		if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
			return count, total, err
		}
		n, err := Get(ctx, sess, f.Path, lp)
		if err != nil {
			return count, total, fmt.Errorf("get %s: %w", f.Path, err)
		}
		if !f.Entry.ModTime.IsZero() {
			_ = os.Chtimes(lp, f.Entry.ModTime, f.Entry.ModTime) // preserve mtime
		}
		count++
		total += n
	}
	return count, total, nil
}

// PutRecursive uploads the local tree at localRoot into remoteRoot (which mirrors
// localRoot: remoteRoot/<rel> for each entry). Returns files and bytes uploaded.
func PutRecursive(ctx context.Context, sess *session.Session, localRoot, remoteRoot string) (int, int64, error) {
	remoteRoot = strings.TrimRight(remoteRoot, "/")
	var count int
	var total int64
	err := filepath.WalkDir(localRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localRoot, p)
		if err != nil {
			return err
		}
		remote := remoteRoot
		if rel != "." {
			remote = remoteRoot + "/" + filepath.ToSlash(rel)
		}
		if d.IsDir() {
			// WalkDir visits parents before children, so dirs are created in order.
			return Mkdir(ctx, sess, remote)
		}
		n, err := Put(ctx, sess, p, remote)
		if err != nil {
			return fmt.Errorf("put %s: %w", p, err)
		}
		count++
		total += n
		return nil
	})
	return count, total, err
}

// RemoveRecursive deletes the remote tree at root (including root). Files and
// directories are removed deepest-first, batched per depth level.
func RemoveRecursive(ctx context.Context, sess *session.Session, root string) error {
	root = strings.TrimRight(root, "/")
	files, dirs, err := Walk(ctx, sess, root)
	if err != nil {
		return err
	}
	nodes := make([]string, 0, len(files)+len(dirs)+1)
	for _, f := range files {
		nodes = append(nodes, f.Path)
	}
	for _, d := range dirs {
		nodes = append(nodes, d.Path)
	}
	nodes = append(nodes, root)

	// Remove deepest paths first so a directory is empty before it is removed.
	sort.Slice(nodes, func(i, j int) bool {
		return strings.Count(nodes[i], "/") > strings.Count(nodes[j], "/")
	})

	// Process one depth level at a time, grouping each level by parent directory.
	i := 0
	for i < len(nodes) {
		depth := strings.Count(nodes[i], "/")
		byDir := map[string][]string{}
		var order []string
		for i < len(nodes) && strings.Count(nodes[i], "/") == depth {
			p := nodes[i]
			dir := path.Dir(p)
			if !strings.HasSuffix(dir, "/") {
				dir += "/"
			}
			if _, ok := byDir[dir]; !ok {
				order = append(order, dir)
			}
			byDir[dir] = append(byDir[dir], path.Base(p))
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
