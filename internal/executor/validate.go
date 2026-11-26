package executor

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/albertcbrown/ard/internal/gitref"
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
	case j.History && j.Stage != "scanner":
		return bad("history is only valid for a scanner job")
	case j.Output && j.Stage != "scanner":
		return bad("output is only valid for a scanner job")
	case j.Network != NetworkOn && j.Network != NetworkAdvisory && j.Network != NetworkNone:
		return bad("network %q", j.Network)
	case j.Timeout <= 0:
		return bad("timeout %s", j.Timeout)
	}
	for name, p := range map[string]string{"repo dir": j.RepoDir, "role dir": j.RoleDir} {
		if !filepath.IsAbs(p) {
			return bad("%s %q is not absolute", name, p)
		}
	}
	for name, p := range map[string]string{"dockerfile": j.Dockerfile} {
		if p != "" && !filepath.IsAbs(p) {
			return bad("%s %q is not absolute", name, p)
		}
	}
	if j.Sidecar != (Sidecar{}) {
		if !validRepo(j.Sidecar.Repo) {
			return bad("sidecar repo %q", j.Sidecar.Repo)
		}
		if err := gitref.Check(j.Sidecar.Ref); err != nil {
			return bad("sidecar ref: %v", err)
		}
	}
	for _, f := range j.Inputs {
		if err := CheckInputPath(f.Path); err != nil {
			return bad("input: %v", err)
		}
	}
	for _, p := range j.Scrub {
		for _, f := range j.Inputs {
			if path.Clean(filepath.ToSlash(f.Path)) == p {
				return bad("scrub: %q is also an input", p)
			}
		}
		if err := checkScrubPath(j.Issue, p); err != nil {
			return bad("scrub: %v", err)
		}
	}
	return nil
}

// checkScrubPath requires p to be a file directly inside .ard/<issue>/.
func checkScrubPath(issue int, p string) error {
	if err := CheckInputPath(p); err != nil {
		return err
	}
	if issue < 1 {
		return fmt.Errorf("path %q needs an issue", p)
	}
	dir := fmt.Sprintf(".ard/%d", issue)
	if path.Clean(p) != p || path.Dir(p) != dir || path.Base(p) == "logs" {
		return fmt.Errorf("path %q must be a file directly under %s/", p, dir)
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

var (
	imageDigestRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)
	imageIDRe     = regexp.MustCompile(`^sha256:[0-9a-f]{12,64}$`)
)

// CheckImageRef validates an agent image reference before it reaches
// `docker run` argv: either <name>@sha256:<64 hex> or a local image ID
// sha256:<12..64 hex>. Tags are refused because they are mutable.
func CheckImageRef(ref string) error {
	if imageDigestRe.MatchString(ref) || imageIDRe.MatchString(ref) {
		return nil
	}
	return fmt.Errorf("%w: want <name>@sha256:<64 hex> or sha256:<hex> (12-64 digits)", ErrImageRef)
}
