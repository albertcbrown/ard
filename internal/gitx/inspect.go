package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The functions in this file are reads: they inspect a worktree or list refs
// on a remote and change nothing, so they are not effects.

// LsRemote lists the refs of the remote url that match pattern (a git
// ls-remote pattern such as "refs/heads/worker/eng-12-*") as ref -> SHA. A 403
// or 404 from the remote is ErrRepoAccess. token may be nil.
func (g *Git) LsRemote(ctx context.Context, dir, url, pattern string, token TokenFunc) (map[string]string, error) {
	var env, secrets []string
	if token != nil {
		tok, err := token(ctx)
		if err != nil {
			return nil, fmt.Errorf("gitx: ls-remote token: %w", err)
		}
		env, secrets = pushEnv(tok, url), []string{tok}
	}
	out, stderr, err := g.run(ctx, dir, env, "", secrets, "ls-remote", url, pattern)
	if err != nil {
		if classifyFetch(stderr, true) == ErrRepoAccess {
			return nil, fmt.Errorf("gitx: ls-remote %s: %w", pattern, ErrRepoAccess)
		}
		return nil, fmt.Errorf("gitx: ls-remote %s: %w", pattern, err)
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && sha != "" && ref != "" {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// NumStat is one entry of git diff --numstat. Binary files have no line
// counts: Binary is set and Added and Deleted are zero.
type NumStat struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// DiffNumstat lists the tracked paths whose working-tree content differs from
// commit base, with their added and deleted line counts. Untracked files are
// not listed. Renames are not detected: a rename is a delete and an add.
func (g *Git) DiffNumstat(ctx context.Context, dir, base string) ([]NumStat, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "--literal-pathspecs", "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", base, "--")
	if err != nil {
		return nil, fmt.Errorf("gitx: numstat since %s: %w", base, err)
	}
	var out2 []NumStat
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
		out2 = append(out2, ns)
	}
	return out2, nil
}

// LineRange is an inclusive range of 1-based line numbers.
type LineRange struct{ From, To int }

// AddedLines returns, per tracked path that differs from commit base in dir's
// working tree, the line ranges of the new file that the change added (the
// "+c,d" side of each git diff -U0 hunk; d == 0 adds nothing). Untracked
// files are not listed: every line of them is new. Binary files have no
// ranges.
func (g *Git) AddedLines(ctx context.Context, dir, base string) (map[string][]LineRange, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "-c", "core.quotePath=false", "--literal-pathspecs",
		"diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", base, "--")
	if err != nil {
		return nil, fmt.Errorf("gitx: added lines since %s: %w", base, err)
	}
	res := map[string][]LineRange{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = ""
		case strings.HasPrefix(line, "+++ "):
			cur = diffPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@ ") && cur != "":
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

// MergeBase returns the best common ancestor of commits a and b.
func (g *Git) MergeBase(ctx context.Context, dir, a, b string) (string, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("gitx: merge-base %s %s: %w", a, b, err)
	}
	return strings.TrimSpace(out), nil
}
