// Package state is the controller's local SQLite state: jobs, attempts, events
// and a small key/value table. It is storage only; policy belongs to callers.
// Local state is not a GitHub side effect, so it does not go through effects;
// dry-run isolation comes from a separate directory (see Dir).
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns <root>/state[-dryrun]/<owner>-<name> for repo "owner/name".
func Dir(root, repo string, dryRun bool) (string, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("state dir: repo %q must be owner/name", repo)
	}
	for _, p := range []string{owner, name} {
		if p == "." || p == ".." || strings.ContainsAny(p, `\`) {
			return "", fmt.Errorf("state dir: repo %q has an invalid part %q", repo, p)
		}
	}
	base := "state"
	if dryRun {
		base = "state-dryrun"
	}
	return filepath.Join(root, base, owner+"-"+name), nil
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
