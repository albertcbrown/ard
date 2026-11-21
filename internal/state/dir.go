// Package state is the controller's local SQLite state: jobs, attempts, events
// and a small key/value table. It is storage only; policy belongs to callers.
// Local state is not a GitHub side effect, so it does not go through effects;
// dry-run isolation comes from a separate directory (see Dir).
package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/albertcbrown/ard/internal/repopath"
)

// Dir returns <root>/state[-dryrun]/<owner>/<repo> for repo "owner/name", both
// parts lowercased (see repopath). Nested so "a-b/c" and "a/b-c" never share.
func Dir(root, repo string, dryRun bool) (string, error) {
	base := "state"
	if dryRun {
		base = "state-dryrun"
	}
	d, err := repopath.Join(filepath.Join(root, base), repo)
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}
	return d, nil
}

// ErrLegacyDir means a pre-#82 flat state directory exists for the repo.
var ErrLegacyDir = errors.New("legacy flat state directory")

// CheckLegacy returns an error wrapping ErrLegacyDir when root/state/ or
// root/state-dryrun/ holds a flat <owner>-<name> directory for repo. It
// creates nothing. Nothing ever writes a file directly into
// state[-dryrun]/<owner>/, so a directory that directly holds a file is a
// legacy one; a directory holding only subdirectories is the owner directory
// of another repo (for example state/a-b/ for a-b/c, when checking a/b).
func CheckLegacy(root, repo string) error {
	owner, name, err := repopath.Parts(repo)
	if err != nil {
		return fmt.Errorf("legacy state check: %w", err)
	}
	want := owner + "-" + name
	for _, base := range []string{"state", "state-dryrun"} {
		nd, err := Dir(root, repo, base == "state-dryrun")
		if err != nil {
			return fmt.Errorf("legacy state check: %w", err)
		}
		baseDir := filepath.Join(root, base)
		entries, err := os.ReadDir(baseDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("legacy state check: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.EqualFold(e.Name(), want) {
				continue
			}
			old := filepath.Join(baseDir, e.Name())
			sub, err := os.ReadDir(old)
			if err != nil {
				return fmt.Errorf("legacy state check: %w", err)
			}
			for _, s := range sub {
				if !s.IsDir() {
					return fmt.Errorf("%w: found %s (from an older ard; it may belong to %s or another repo with the same dashed name). This version uses %s. Move or remove it by hand; ard does not migrate it (#82)",
						ErrLegacyDir, old, repo, nd)
				}
			}
		}
	}
	return nil
}

// EnsureDir creates dir with mode 0700.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod state dir %s: %w", dir, err)
	}
	return nil
}
