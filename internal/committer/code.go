package committer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// CodeFile is one worker change. Exactly one of Src or Delete is set.
type CodeFile struct {
	Path   string // canonical repo-relative slash path; CheckCodePath must accept it
	Src    string // absolute host path; read with readSrc (no symlinks, regular, <= MaxFileBytes)
	Exec   bool   // commit as 100755 (only with Src)
	Delete bool
}

// CodeRequest is one worker code commit on a worker/ branch.
type CodeRequest struct {
	Issue  int        // > 0 (dry-run: < 0 allowed, synthetic result, as Request)
	Role   string     // ^[a-z0-9][a-z0-9-]*$
	JobID  string     // required: the Ard-Job trailer
	Type   string     // ^[a-z]{1,16}$, e.g. feat, fix, chore
	Title  string     // untrusted issue title
	Body   string     // untrusted summary, already redacted by the caller
	Base   string     // 40-hex SHA the job's worktree started from
	Create bool       // the branch must not exist: cut it at Base
	Files  []CodeFile // 1..MaxFiles, unique paths
}

// ErrBranchExists means a Create found the branch already on the remote.
var ErrBranchExists = errors.New("committer: branch already exists on the remote")

const (
	maxCodeBodyRunes = 4000
	zeroSHA          = "0000000000000000000000000000000000000000"
)

var (
	typeRe = regexp.MustCompile(`^[a-z]{1,16}$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// blankRunRe matches three or more consecutive newlines (two blank lines).
	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

// CodeMessage builds:
//
//	<type>: <title> (#<issue>)
//
//	<body>
//
//	Ard-Job: <jobID>
//
// Title: control characters, newlines and other whitespace collapse to single
// spaces; skip-CI tokens and "skip-checks:" are defused as in Message; closing
// references are neutralised; the title part is cut (with "…") so the first
// line is at most 72 runes and always ends in " (#<issue>)". An empty title
// after cleaning is ErrInvalid.
// Body: \r\n and \r become \n; other control and format (Cf) characters are
// dropped; skip-CI tokens defused and references neutralised; runs of more
// than two blank lines squeezed; cut to 4,000 runes at a line end; trimmed.
// An empty body leaves out its paragraph. The trailer paragraph is always last.
// No [skip ci]: PR-branch commits always run CI.
func CodeMessage(typ, title string, issue int, body, jobID string) (string, error) {
	switch {
	case !typeRe.MatchString(typ):
		return "", fmt.Errorf("%w: bad type %q", ErrInvalid, typ)
	case !jobRe.MatchString(jobID):
		return "", fmt.Errorf("%w: bad or missing job id", ErrInvalid)
	case issue <= 0:
		return "", fmt.Errorf("%w: issue must be positive", ErrInvalid)
	}
	t := strings.Join(strings.FieldsFunc(title, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}), " ")
	t = NeutraliseRefs(defuseSkip(t))
	if t == "" {
		return "", fmt.Errorf("%w: empty title", ErrInvalid)
	}
	prefix, suffix := typ+": ", fmt.Sprintf(" (#%d)", issue)
	if avail := maxSummaryRunes - utf8.RuneCountInString(prefix+suffix); utf8.RuneCountInString(t) > avail {
		t = strings.TrimSpace(string([]rune(t)[:avail-1])) + "…"
	}
	msg := prefix + t + suffix

	b := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	b = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, b)
	b = NeutraliseRefs(defuseSkip(b))
	b = blankRunRe.ReplaceAllString(b, "\n\n")
	if r := []rune(b); len(r) > maxCodeBodyRunes {
		b = string(r[:maxCodeBodyRunes])
		if i := strings.LastIndex(b, "\n"); i >= 0 {
			b = b[:i]
		}
	}
	if b = strings.TrimSpace(b); b != "" {
		msg += "\n\n" + b
	}
	return msg + "\n\nArd-Job: " + jobID, nil
}

func defuseSkip(s string) string {
	s = skipRe.ReplaceAllString(s, "(skip-ci)")
	return skipChecksRe.ReplaceAllString(s, "skip-checks -")
}

// CommitCode commits req to branch and pushes it. With Create it cuts the
// branch at req.Base with a push whose lease expects the ref to be absent
// (ErrBranchExists if it is not); otherwise it updates the existing branch
// through the lane's fetch, apply and retry loop (ErrBranchGone if missing)
// and refuses with ErrConflict to overwrite a path that changed on the branch
// since req.Base. Nothing is ever force-pushed over an existing ref.
func (b *Branches) CommitCode(ctx context.Context, branch string, req CodeRequest) (res Result, err error) {
	l, err := b.laneFor(branch)
	if err != nil {
		return Result{}, err
	}
	dry := l.fx.DryRun()
	issue := req.Issue
	switch {
	case issue < 0 && dry:
		issue = -issue
	case issue <= 0:
		return Result{}, fmt.Errorf("commit code issue %d: %w: issue must be positive", req.Issue, ErrInvalid)
	}
	s, err := codeSnapshot(req, issue)
	if err != nil {
		return Result{}, fmt.Errorf("commit code issue %d (%s): %w", req.Issue, req.Role, err)
	}
	start := time.Now()
	defer func() {
		attrs := []any{"issue", req.Issue, "role", req.Role, "branch", branch, "job", req.JobID,
			"files", len(s.files), "created", req.Create, "duration", time.Since(start).String()}
		if err != nil {
			l.log.WarnContext(ctx, "committer.failed", append(attrs, "err", err.Error())...)
			return
		}
		l.log.InfoContext(ctx, "committer.commit_code", append(attrs, "sha", res.SHA, "noop", res.NoOp, "synthetic", res.Synthetic, "attempts", res.Attempts)...)
	}()
	if req.Issue < 0 { // synthetic issue: validated, nothing touches git
		return Result{SHA: strings.Repeat("0", 40), Synthetic: true}, nil
	}
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return Result{}, fmt.Errorf("commit code issue %d: %w", req.Issue, ctx.Err())
	}
	defer func() { <-l.sem }()
	if l.isClosed() {
		return Result{}, ErrClosed
	}
	if req.Create {
		res, err = l.create(ctx, s)
	} else {
		res, err = l.run(ctx, s)
	}
	if err != nil {
		err = fmt.Errorf("commit code issue %d (%s): %w", req.Issue, req.Role, err)
	}
	return res, err
}

// codeSnapshot validates req and reads its sources into memory. Path and
// source problems come back together in one *GuardError, shape problems as
// ErrInvalid.
func codeSnapshot(req CodeRequest, issue int) (snap, error) {
	switch {
	case !roleRe.MatchString(req.Role):
		return snap{}, fmt.Errorf("%w: bad role %q", ErrInvalid, req.Role)
	case !shaRe.MatchString(req.Base):
		return snap{}, fmt.Errorf("%w: base must be a 40-hex commit SHA", ErrInvalid)
	case len(req.Files) == 0:
		return snap{}, fmt.Errorf("%w: no files", ErrInvalid)
	case len(req.Files) > MaxFiles:
		return snap{}, fmt.Errorf("%w: %d files, max %d", ErrInvalid, len(req.Files), MaxFiles)
	}
	msg, err := CodeMessage(req.Type, req.Title, issue, req.Body, req.JobID)
	if err != nil {
		return snap{}, err
	}
	s := snap{msg: msg, base: req.Base}
	ge := &GuardError{}
	seen := map[string]bool{}
	for _, f := range req.Files {
		if err := CheckCodePath(issue, f.Path); err != nil {
			ge.add(f.Path, err.Error())
			continue
		}
		if seen[f.Path] {
			return snap{}, fmt.Errorf("%w: duplicate path %q", ErrInvalid, f.Path)
		}
		seen[f.Path] = true
		switch {
		case f.Delete && f.Src != "":
			return snap{}, fmt.Errorf("%w: %q has both Src and Delete", ErrInvalid, f.Path)
		case !f.Delete && f.Src == "":
			return snap{}, fmt.Errorf("%w: %q needs one of Src or Delete", ErrInvalid, f.Path)
		case f.Delete && f.Exec:
			return snap{}, fmt.Errorf("%w: %q: Exec with Delete", ErrInvalid, f.Path)
		}
		sf := snapFile{path: f.Path, exec: f.Exec, del: f.Delete}
		if f.Src != "" {
			data, err := readSrc(f.Src)
			var pe *guardReason
			switch {
			case errors.As(err, &pe):
				ge.add(f.Path, pe.msg)
				continue
			case err != nil:
				return snap{}, fmt.Errorf("%w: src %q: %v", ErrInvalid, f.Src, err)
			}
			sf.data = data
		}
		s.files = append(s.files, sf)
		s.declared = append(s.declared, f.Path)
	}
	if len(ge.Paths) > 0 {
		return snap{}, ge
	}
	return s, nil
}

// create cuts the branch at s.base, commits s on top and pushes it with a
// lease that expects the ref to be absent. There is no retry.
func (l *lane) create(ctx context.Context, s snap) (Result, error) {
	if err := os.MkdirAll(l.cfg.WorkDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir workdir: %w", err)
	}
	g, repo := l.g, l.cfg.RepoDir
	if err := g.WorktreeRemove(ctx, repo, l.wt); err != nil {
		return Result{}, err
	}
	if err := g.WorktreeAdd(ctx, repo, l.wt, s.base); err != nil {
		return Result{}, err
	}
	defer func() {
		if err := g.WorktreeRemove(context.WithoutCancel(ctx), repo, l.wt); err != nil {
			l.log.WarnContext(ctx, "committer.cleanup_failed", slog.String("branch", l.branch), slog.String("err", err.Error()))
		}
	}()
	res, _, err := l.attempt(ctx, s, s.base, true)
	res.Attempts = 1
	return res, err
}
