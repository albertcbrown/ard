package committer

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/gitx"
)

// MaxFileBytes is the 1 MiB log cap from CLAUDE.md.
const MaxFileBytes = 1 << 20

var pathRe = regexp.MustCompile(`^\.ard/([1-9][0-9]*)/[^/]+(/[^/]+)*$`)

// CheckPath reports why p may not be written for issue, or nil. Paths must
// already be canonical: they are rejected, not normalised.
func CheckPath(issue int, p string) error {
	if p == "" {
		return reason("empty path")
	}
	for _, r := range p {
		if r == '\\' || r < 0x20 || r == 0x7f {
			return reason("backslash or control character")
		}
	}
	if path.Clean(p) != p {
		return reason("not canonical")
	}
	m := pathRe.FindStringSubmatch(p)
	if m == nil {
		return reason("outside .ard/<issue#>/")
	}
	if n, _ := strconv.Atoi(m[1]); n != issue {
		return reason(fmt.Sprintf("belongs to issue %s, not %d", m[1], issue))
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, ".git") {
			return reason("contains .git")
		}
	}
	return nil
}

type reason string

func (r reason) Error() string { return string(r) }

// CheckStaged verifies the index after staging: every staged path was
// declared, and no staged entry is a symlink or a gitlink. The staged set may
// be smaller than the declared set (unchanged files stage nothing).
func CheckStaged(declared []string, changes []gitx.Change) error {
	want := make(map[string]bool, len(declared))
	for _, d := range declared {
		want[d] = true
	}
	ge := &GuardError{}
	for _, c := range changes {
		switch {
		case !want[c.Path]:
			ge.add(c.Path, "staged but not requested")
		case c.NewMode != "100644" && c.NewMode != "100755" && c.NewMode != "000000":
			ge.add(c.Path, "unsupported mode "+c.NewMode)
		}
	}
	if len(ge.Paths) > 0 {
		return ge
	}
	return nil
}
