package executor

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/albertcbrown/ard/internal/repopath"
)

var (
	idRE   = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)
	roleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

var stages = map[string]bool{"scanner": true, "triage": true, "worker": true, "review": true}

// Validate checks the job's fields.
func (j Job) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidJob, fmt.Sprintf(format, a...))
	}
	switch {
	case !idRE.MatchString(j.ID):
		return bad("id %q", j.ID)
	case !validRepo(j.Repo):
		return bad("repo %q", j.Repo)
	case j.Issue < 0:
		return bad("issue %d", j.Issue)
	case !stages[j.Stage]:
		return bad("stage %q", j.Stage)
	case !roleRE.MatchString(j.Role):
		return bad("role %q", j.Role)
	case j.Ref == "" || strings.HasPrefix(j.Ref, "-"):
		return bad("ref %q", j.Ref)
	case j.TrustedInput && j.Stage != "scanner":
		return bad("trusted input is only valid for a scanner job")
	case j.Timeout <= 0:
		return bad("timeout %s", j.Timeout)
	}
	for name, p := range map[string]string{"repo dir": j.RepoDir, "role dir": j.RoleDir} {
		if !filepath.IsAbs(p) {
			return bad("%s %q is not absolute", name, p)
		}
	}
	for name, p := range map[string]string{"dockerfile": j.Dockerfile, "sidecar dir": j.SidecarDir} {
		if p != "" && !filepath.IsAbs(p) {
			return bad("%s %q is not absolute", name, p)
		}
	}
	for _, f := range j.Inputs {
		if err := CheckInputPath(f.Path); err != nil {
			return bad("input: %v", err)
		}
	}
	return nil
}

func validRepo(repo string) bool {
	_, _, err := repopath.Parts(repo)
	return err == nil
}

// CheckInputPath rejects absolute paths, "..", and anything under .git.
func CheckInputPath(p string) error {
	if p == "" || path.IsAbs(p) || filepath.IsAbs(p) {
		return fmt.Errorf("path %q must be relative", p)
	}
	c := path.Clean(filepath.ToSlash(p))
	if c == ".." || strings.HasPrefix(c, "../") || strings.Contains(filepath.ToSlash(p), "..") {
		return fmt.Errorf("path %q escapes the worktree", p)
	}
	if c == ".git" || strings.HasPrefix(c, ".git/") {
		return fmt.Errorf("path %q is inside .git", p)
	}
	return nil
}
