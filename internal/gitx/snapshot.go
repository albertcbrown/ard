package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// emptyTree is the id of the empty tree. Attributes are read from it, so no
// .gitattributes (the agent's, or one it committed on an earlier round) can
// turn on a filter, an eol conversion or a diff driver.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// gitDirOf is the git directory of the controller's repository at repoDir: a
// bare clone is its own, a normal clone keeps it in .git.
func gitDirOf(repoDir string) string {
	if fi, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil && fi.IsDir() {
		return filepath.Join(repoDir, ".git")
	}
	return repoDir
}

// Snapshot is a view of what an agent left in a worktree, taken without
// trusting anything the agent controls: not the worktree's .git file or gitdir
// (its config can name filters, textconv, fsmonitor or hooks that would run
// with the controller's environment), not its index (skip-worktree and
// assume-unchanged bits hide edits) and not its .gitattributes.
//
// Every command runs as git --git-dir=<controller repo> --work-tree=<wt> with
// GIT_INDEX_FILE pointing at a private index that read-tree filled from the
// base commit, and git add -A then records the disk. What the diff shows is
// therefore the disk's bytes against the base commit, the same bytes a
// committer reading the files will push.
type Snapshot struct {
	g      *Git
	gitDir string
	wt     string
	base   string
	tmp    string
	idx    string
}

// Snapshot builds the private index of wt against commit base. repoDir is the
// controller's repository, where base lives. Close removes the index.
func (g *Git) Snapshot(ctx context.Context, repoDir, wt, base string) (*Snapshot, error) {
	if repoDir == "" || wt == "" || base == "" {
		return nil, fmt.Errorf("gitx: snapshot: repo dir, worktree and base are required")
	}
	tmp, err := os.MkdirTemp("", "ard-index-")
	if err != nil {
		return nil, fmt.Errorf("gitx: snapshot: %w", err)
	}
	s := &Snapshot{g: g, gitDir: gitDirOf(repoDir), wt: wt, base: base, tmp: tmp, idx: filepath.Join(tmp, "index")}
	if _, err := s.git(ctx, "read-tree", base); err != nil {
		s.Close()
		return nil, fmt.Errorf("gitx: snapshot read-tree %.12s: %w", base, err)
	}
	if _, err := s.git(ctx, "add", "-A", "--", "."); err != nil {
		s.Close()
		return nil, fmt.Errorf("gitx: snapshot add: %w", err)
	}
	return s, nil
}

// Close removes the private index.
func (s *Snapshot) Close() error { return os.RemoveAll(s.tmp) }

// git runs one command against the private index.
func (s *Snapshot) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{
		"--git-dir=" + s.gitDir, "--work-tree=" + s.wt,
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "diff.external=",
		"-c", "core.autocrlf=false", "-c", "core.safecrlf=false", "-c", "core.quotePath=false",
		"-c", "core.untrackedCache=false", "-c", "core.sparseCheckout=false",
		"--literal-pathspecs"}, args...)
	env := []string{"GIT_INDEX_FILE=" + s.idx, "GIT_ATTR_SOURCE=" + emptyTree, "GIT_OPTIONAL_LOCKS=0"}
	// The command's working directory is the controller's repository, never the
	// agent's tree.
	out, _, err := s.g.run(ctx, filepath.Dir(s.gitDir), env, "", nil, full...)
	return out, err
}

// Changed lists the paths under pathspecs (literal, directory prefixes
// allowed; none means every path) that differ between the base commit and the
// disk: changes, deletions and new files that are not ignored. Mode is the
// index mode of the new side.
func (s *Snapshot) Changed(ctx context.Context, paths ...string) ([]PathChange, error) {
	args := append([]string{"diff", "--cached", "--raw", "-z", "--no-renames", "--no-textconv", "--no-ext-diff", s.base, "--"}, paths...)
	out, err := s.git(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: changed since %.12s: %w", s.base, err)
	}
	var cs []PathChange
	tok := strings.Split(out, "\x00")
	for i := 0; i+1 < len(tok); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(tok[i], ":"))
		if len(meta) < 5 {
			if tok[i] == "" {
				continue
			}
			return nil, fmt.Errorf("gitx: changed since %.12s: bad record %q", s.base, tok[i])
		}
		c := PathChange{Path: tok[i+1], Deleted: meta[4][0] == 'D'}
		if !c.Deleted {
			c.Mode = meta[1]
		}
		cs = append(cs, c)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Path < cs[j].Path })
	return cs, nil
}

// Numstat lists added and deleted line counts of the disk against commit
// against ("" means the base commit); new files count every line.
func (s *Snapshot) Numstat(ctx context.Context, against string) ([]NumStat, error) {
	if against == "" {
		against = s.base
	}
	out, err := s.git(ctx, "diff", "--cached", "--numstat", "-z", "--no-renames", "--no-textconv", "--no-ext-diff", against, "--")
	if err != nil {
		return nil, fmt.Errorf("gitx: numstat since %.12s: %w", against, err)
	}
	var res []NumStat
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 {
			return nil, fmt.Errorf("gitx: numstat: bad record %q", rec)
		}
		ns := NumStat{Path: f[2]}
		if f[0] == "-" && f[1] == "-" {
			ns.Binary = true
		} else {
			a, err1 := strconv.Atoi(f[0])
			d, err2 := strconv.Atoi(f[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("gitx: numstat: bad counts in %q", rec)
			}
			ns.Added, ns.Deleted = a, d
		}
		res = append(res, ns)
	}
	return res, nil
}

// AddedLines returns, per path that differs from the base commit, the line
// ranges of the new file that the change added (the "+c,d" side of each -U0
// hunk; d == 0 adds nothing). A new file is one range over every line.
func (s *Snapshot) AddedLines(ctx context.Context) (map[string][]LineRange, error) {
	out, err := s.git(ctx, "diff", "--cached", "-U0", "--no-color", "--no-textconv", "--no-ext-diff", "--no-renames",
		"--src-prefix=a/", "--dst-prefix=b/", s.base, "--")
	if err != nil {
		return nil, fmt.Errorf("gitx: added lines since %.12s: %w", s.base, err)
	}
	return parseAdded(out)
}

// parseAdded reads a -U0 diff. A "+++ " line names the file only inside a
// file's header (between "diff --git" and its first "@@"): inside a hunk the
// same text is a line the agent added.
func parseAdded(out string) (map[string][]LineRange, error) {
	res := map[string][]LineRange{}
	cur, inHeader := "", false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur, inHeader = "", true
		case inHeader && strings.HasPrefix(line, "+++ "):
			cur = diffPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@ "):
			inHeader = false
			if cur == "" {
				continue
			}
			r, ok, err := hunkAdded(line)
			if err != nil {
				return nil, err
			}
			if ok {
				res[cur] = append(res[cur], r)
			}
		}
	}
	return res, nil
}

// diffPath returns the path of a "+++ b/<path>" header value, or "" for
// /dev/null.
func diffPath(s string) string {
	s = strings.TrimSuffix(s, "\t") // git adds a tab after names with spaces
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		}
	}
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, "b/")
}

// hunkAdded parses the new-file side of a "@@ -a,b +c,d @@" header.
func hunkAdded(h string) (LineRange, bool, error) {
	i := strings.Index(h, " +")
	if i < 0 {
		return LineRange{}, false, fmt.Errorf("gitx: bad hunk header %q", h)
	}
	spec, _, _ := strings.Cut(h[i+2:], " ")
	startS, countS, hasCount := strings.Cut(spec, ",")
	start, err := strconv.Atoi(startS)
	if err != nil {
		return LineRange{}, false, fmt.Errorf("gitx: bad hunk header %q", h)
	}
	count := 1
	if hasCount {
		if count, err = strconv.Atoi(countS); err != nil {
			return LineRange{}, false, fmt.Errorf("gitx: bad hunk header %q", h)
		}
	}
	if count == 0 {
		return LineRange{}, false, nil
	}
	return LineRange{From: start, To: start + count - 1}, true, nil
}
