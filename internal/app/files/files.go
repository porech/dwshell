// Package files implements the DWService "filesystem" app over an agent session:
// directory listing and single-file download/upload. It layers on the generic
// session command channel (metadata) and transfer primitives (bytes).
package files

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/porech/dwshell/internal/session"
)

const module = "filesystem"

// Entry is one item in a remote directory listing.
type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	Rights  string // octal, e.g. "755"
	Owner   string
	Group   string
}

// Open loads the filesystem app on the agent session. The agent may lazily
// download the app on first use, so a failed first load is retried once.
func Open(ctx context.Context, sess *session.Session) error {
	_, err := sess.Execute(ctx, "core", "load_app", map[string]string{"name": module})
	if err == nil {
		return nil
	}
	// Retry once after a short delay for the lazy-download case.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	if _, err = sess.Execute(ctx, "core", "load_app", map[string]string{"name": module}); err != nil {
		return fmt.Errorf("load filesystem app: %w", err)
	}
	return nil
}

type listResponse struct {
	Items []struct {
		Name         string `json:"Name"`
		LastModified int64  `json:"LastModified"`
		Length       int64  `json:"Length"`
		Rights       string `json:"Rights"`
		Owner        string `json:"Owner"`
		Group        string `json:"Group"`
	} `json:"items"`
}

// List returns the entries of a remote directory.
func List(ctx context.Context, sess *session.Session, path string) ([]Entry, error) {
	raw, err := sess.Execute(ctx, module, "list", map[string]string{"path": path})
	if err != nil {
		return nil, err
	}
	return parseList(raw)
}

// parseList decodes a filesystem "list" response into entries.
func parseList(raw []byte) ([]Entry, error) {
	var lr listResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}
	entries := make([]Entry, 0, len(lr.Items))
	for _, it := range lr.Items {
		isDir := strings.HasPrefix(it.Name, "D:")
		name := it.Name
		if len(name) >= 2 && (name[1] == ':') {
			name = name[2:]
		}
		e := Entry{
			Name:   name,
			IsDir:  isDir,
			Size:   it.Length,
			Rights: it.Rights,
			Owner:  it.Owner,
			Group:  it.Group,
		}
		if it.LastModified > 0 {
			e.ModTime = time.UnixMilli(it.LastModified)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// RemoveGroup is a set of file names to remove from one directory.
type RemoveGroup struct {
	Dir   string
	Names []string
}

// Remove deletes one or more names inside a single remote directory. It does not
// recurse into subdirectories (planned).
func Remove(ctx context.Context, sess *session.Session, dir string, names []string) error {
	return RemoveMany(ctx, sess, []RemoveGroup{{Dir: dir, Names: names}})
}

// RemoveMany removes several directory groups in one batched request. It returns
// an error listing every name that could not be removed.
func RemoveMany(ctx context.Context, sess *session.Session, groups []RemoveGroup) error {
	if len(groups) == 0 {
		return nil
	}
	cmds := make([]session.Command, len(groups))
	for i, g := range groups {
		filesJSON, err := json.Marshal(g.Names)
		if err != nil {
			return err
		}
		cmds[i] = session.Command{
			Module:  module,
			Command: "remove",
			Params:  map[string]string{"path": g.Dir, "files": string(filesJSON)},
		}
	}
	results, err := sess.ExecuteBatch(ctx, cmds)
	if err != nil {
		return err
	}
	var failed []string
	for i, r := range results {
		if r.Err != nil {
			failed = append(failed, groups[i].Names...) // whole command failed
			continue
		}
		failed = append(failed, parseRemoveFailures(r.Data)...)
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not remove: %s", strings.Join(failed, ", "))
	}
	return nil
}

// parseRemoveFailures returns the names the agent could not remove. The response
// is {"items":[{"Name":"<status>:<name>"}]} where status "K" = removed and
// "E" = error.
func parseRemoveFailures(raw []byte) []string {
	var resp struct {
		Items []struct {
			Name string `json:"Name"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return nil
	}
	var failed []string
	for _, it := range resp.Items {
		if strings.HasPrefix(it.Name, "E:") {
			failed = append(failed, strings.TrimPrefix(it.Name, "E:"))
		}
	}
	return failed
}

// Mkdir creates the remote directory fullPath. An "already exists" error is
// treated as success so it is idempotent.
func Mkdir(ctx context.Context, sess *session.Session, fullPath string) error {
	dir := path.Dir(fullPath)
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	_, err := sess.Execute(ctx, module, "makedir", map[string]string{
		"path": dir,
		"name": path.Base(fullPath),
	})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "exist") {
		return nil
	}
	return err
}

// Get downloads remotePath into localPath (or stdout when localPath is "-").
func Get(ctx context.Context, sess *session.Session, remotePath, localPath string) (int64, error) {
	rc, _, err := sess.Download(ctx, module, remotePath)
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	var dst io.Writer
	if localPath == "-" {
		dst = os.Stdout
	} else {
		f, err := os.Create(localPath)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		dst = f
	}
	return io.Copy(dst, rc)
}

// Put uploads localPath (or stdin when localPath is "-") to remotePath.
func Put(ctx context.Context, sess *session.Session, localPath, remotePath string) (int64, error) {
	var src io.Reader
	var size int64 = -1
	if localPath == "-" {
		src = os.Stdin
	} else {
		f, err := os.Open(localPath)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		if st, err := f.Stat(); err == nil {
			size = st.Size()
		}
		src = f
	}
	if err := sess.Upload(ctx, module, remotePath, src); err != nil {
		return 0, err
	}
	return size, nil
}
