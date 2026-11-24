// Package workspace is the filesystem half of an executor: the job worktree,
// home, config dir, inputs, sidecar export, logs and their removal. It is
// shared by the local executor and, for Populate and MakeHome, by code that
// runs inside a pod.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/executor"
)

// ConfigDirName is the per-job agent config dir inside the job home. Under
// HOME, the CLI's default ~/.claude resolves to the same fresh dir even if
// the config-dir variable were dropped.
const ConfigDirName = ".claude"

// PopulateSpec says what to do to a checked-out tree.
type PopulateSpec struct {
	Tree   string          // the worktree
	Home   string          // job home; "" = make none
	Scrub  []string        // tree-relative paths to remove
	Inputs []executor.File // files to write into the tree
}

// Populated reports what Populate did.
type Populated struct {
	Scrubbed  []string
	ConfigDir string // "" when Home was ""
}

// Populate scrubs paths, writes inputs, then makes the home, in that order.
func Populate(s PopulateSpec) (Populated, error) {
	var p Populated
	if err := scrub(s.Tree, s.Scrub); err != nil {
		return p, err
	}
	p.Scrubbed = append([]string(nil), s.Scrub...)
	if len(s.Inputs) > 0 {
		// os.Root refuses to follow symlinks out of the worktree, so a
		// committed symlink at an input path cannot redirect the write.
		r, err := os.OpenRoot(s.Tree)
		if err != nil {
			return p, fmt.Errorf("open worktree: %w", err)
		}
		defer r.Close()
		for _, f := range s.Inputs {
			rel := filepath.FromSlash(f.Path)
			if err := r.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
				return p, fmt.Errorf("write input %s: %w", f.Path, err)
			}
			if err := rejectSymlink(r, rel); err != nil {
				return p, fmt.Errorf("write input %s: %w", f.Path, err)
			}
			if err := r.WriteFile(rel, f.Data, 0o644); err != nil {
				return p, fmt.Errorf("write input %s: %w", f.Path, err)
			}
		}
	}
	if s.Home != "" {
		cfg, err := MakeHome(s.Home)
		if err != nil {
			return p, err
		}
		p.ConfigDir = cfg
	}
	return p, nil
}

// MakeHome creates home/tmp and the config dir inside home, both 0700 past
// the umask, and returns the config dir.
func MakeHome(home string) (configDir string, err error) {
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		return "", fmt.Errorf("create home: %w", err)
	}
	cfg := filepath.Join(home, ConfigDirName)
	if err := os.Mkdir(cfg, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	if err := os.Chmod(cfg, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return cfg, nil
}

// scrub removes each path from the worktree: a regular file or a symlink
// (the link itself). A missing path is fine. It fails if the parent directory
// is a symlink (os.Root would follow it inside the worktree) and never removes
// a directory.
func scrub(wt string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	r, err := os.OpenRoot(wt)
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
	defer r.Close()
	for _, p := range paths {
		rel := filepath.FromSlash(p)
		parent, err := r.Lstat(filepath.Dir(rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("scrub %s: %w", p, err)
		case parent.Mode()&fs.ModeSymlink != 0 || !parent.IsDir():
			return fmt.Errorf("scrub %s: parent %s is not a plain directory", p, filepath.Dir(p))
		}
		st, err := r.Lstat(rel)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("scrub %s: %w", p, err)
		case !st.Mode().IsRegular() && st.Mode()&fs.ModeSymlink == 0:
			return fmt.Errorf("scrub %s: not a file", p)
		}
		if err := r.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("scrub %s: %w", p, err)
		}
	}
	return nil
}

// rejectSymlink fails when rel already exists as a symlink: even a link that
// stays inside the worktree would let a branch swap what an input resolves to.
func rejectSymlink(r *os.Root, rel string) error {
	if st, err := r.Lstat(rel); err == nil && st.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", rel)
	}
	return nil
}
