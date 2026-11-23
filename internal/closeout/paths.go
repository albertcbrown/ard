package closeout

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/albertcbrown/ard/internal/redact"
)

// HostPaths are the controller host's own paths, the same for every job.
type HostPaths struct {
	Home    string // the operator's home directory -> "~"
	ArdRoot string // the executor root, <Home>/.ard -> "~/.ard"
	TempDir string // os.TempDir() -> "$TMPDIR"; skipped when shared (/tmp, /var/tmp, /private/tmp)
}

// sharedTemp are temp dirs that name no one and are used by everything.
var sharedTemp = []string{"/tmp", "/var/tmp", "/private/tmp"}

// PathRules lists the host paths of one job, each in its given and its
// symlink-resolved form. Empty, relative and "/" paths are skipped. The
// order is worktree, job home, ard root, home, temp dir; WithPaths sorts by
// length. Placeholders: /work, /ard-home, ~/.ard, ~, $TMPDIR.
func PathRules(worktree, jobHome string, host HostPaths) []redact.PathRule {
	var out []redact.PathRule
	seen := map[string]bool{}
	add := func(name, p, to string) {
		for _, f := range resolveForms(p) {
			if seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, redact.PathRule{Name: name, From: f, To: to})
		}
	}
	add("path.work", worktree, "/work")
	add("path.job-home", jobHome, "/ard-home")
	add("path.ard-root", host.ArdRoot, "~/.ard")
	add("path.home", host.Home, "~")
	if t := host.TempDir; t != "" && !slices.Contains(sharedTemp, filepath.Clean(t)) {
		if forms := resolveForms(t); !slices.ContainsFunc(forms, func(f string) bool { return slices.Contains(sharedTemp, f) }) {
			add("path.tmp", t, "$TMPDIR")
		}
	}
	return out
}

// resolveForms returns the cleaned p and, when it differs, its symlink-
// resolved form. A path that does not exist is resolved through its longest
// existing ancestor. It returns nil for an empty, relative or root path.
// Resolution errors only drop the resolved form.
func resolveForms(p string) []string {
	if p == "" || !filepath.IsAbs(p) {
		return nil
	}
	p = filepath.Clean(p)
	if p == "/" {
		return nil
	}
	out := []string{p}
	rest, dir := "", p
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			if res := filepath.Join(r, rest); res != p && res != "/" {
				out = append(out, res)
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
	return out
}

// JobRedactor is the production Deps.NewRedactor: the credential values in
// environ() (redact.FromEnviron) plus PathRules(j.Worktree, j.Home, host).
// A bad path rule fails the build, so close-out commits nothing.
func JobRedactor(environ func() []string, host HostPaths) func(Job) (Redactor, error) {
	return func(j Job) (Redactor, error) {
		s, err := redact.FromEnviron(environ()).WithPaths(PathRules(j.Worktree, j.Home, host)...)
		if err != nil {
			return nil, fmt.Errorf("closeout: path rules: %w", err)
		}
		return redact.New(s, redact.Options{})
	}
}
