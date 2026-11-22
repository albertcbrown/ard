package closeout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/redact"
)

const (
	maxFiles     = 100
	maxFileBytes = 1 << 20
)

// Prepare reads a finished job's worktree and host log and decides what
// close-out will do. It changes nothing outside Deps.StagingDir: the cleaned
// bytes of every file to commit are staged there, and the returned Record
// (phase prepared) holds the plan. From here on Resume uses only the staged
// bytes and the plan, never the worktree.
//
// Only files under .ard/<issue#>/ that changed since the worktree's base
// commit are collected, so a newer issue.md that landed on main during the
// job is never overwritten. Every file and the log pass through the redactor;
// a hit or an error withholds the file (fail closed). A failed job commits
// only its log.
func Prepare(ctx context.Context, j Job, d Deps) (Record, error) {
	if err := j.validate(); err != nil {
		return Record{}, err
	}
	if d.NewRedactor == nil || d.Changed == nil || d.StagingDir == "" {
		return Record{}, errors.New("closeout: Deps needs NewRedactor, Changed and StagingDir")
	}
	red, err := d.NewRedactor(j)
	if err != nil {
		return Record{}, fmt.Errorf("closeout job %s: redactor: %w", j.ID, err)
	}
	stage := filepath.Join(d.StagingDir, j.ID)
	if err := os.RemoveAll(stage); err != nil {
		return Record{}, fmt.Errorf("closeout job %s: clear staging: %w", j.ID, err)
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return Record{}, fmt.Errorf("closeout job %s: staging: %w", j.ID, err)
	}
	n := j.Issue
	if n < 0 { // dry-run issues are synthetic and negative
		n = -n
	}
	dir := fmt.Sprintf(".ard/%d/", n)
	rolePath := dir + j.Role + ".md"

	// 1. Decide whether the agent's result is usable.
	var (
		rf       RoleFile
		failed   string
		cands    []candidate
		roleSeen bool
	)
	switch {
	case j.Cause.Kind != "":
		failed = string(j.Cause.Kind)
		if j.Cause.Detail != "" {
			failed += ": " + j.Cause.Detail
		}
	case j.ExitCode != 0:
		failed = fmt.Sprintf("agent exited %d", j.ExitCode)
	default:
		rf, failed, err = readResult(j, rolePath, d.log())
		if err != nil {
			return Record{}, err
		}
	}
	if failed == "" {
		cands, failed, err = collect(ctx, j, d, dir)
		if err != nil {
			return Record{}, err
		}
		for _, c := range cands {
			if c.path == rolePath && !c.delete {
				roleSeen = true
			}
		}
	}

	// 2. Redact and stage.
	var (
		files   []FileRef
		hit     string // first non-log file that was flagged, with rule ids
		summary string
	)
	if failed == "" {
		for _, c := range cands {
			if c.delete {
				files = append(files, FileRef{Path: c.path, Delete: true})
				continue
			}
			res, terr := red.Text(ctx, c.data)
			if terr != nil || res.Flagged {
				hit = fmt.Sprintf("secret scan %s %s", verdictWord(terr), c.path)
				if res.Flagged {
					hit += " (rules: " + ruleList(res.Findings) + ")"
				}
				break
			}
			ref, werr := stageFile(stage, c.path, res.Clean)
			if werr != nil {
				return Record{}, fmt.Errorf("closeout job %s: %w", j.ID, werr)
			}
			files = append(files, ref)
			if c.path == rolePath {
				if crf, perr := Parse(res.Clean); perr == nil {
					summary = crf.Summary
				}
			}
		}
		if hit != "" {
			files = nil
		}
	}

	// 3. The log.
	logWithheld, logReason := false, ""
	var logRef *FileRef
	var tail string
	if j.LogPath != "" {
		logRef, tail, logReason, err = prepareLog(ctx, red, j, n, stage)
		if err != nil {
			return Record{}, err
		}
		logWithheld = logRef == nil
	}
	if logRef != nil {
		files = append(files, *logRef)
	}

	// 4. The plan.
	var plan Plan
	switch {
	case failed != "" && j.Cause.Retry:
		plan = retryPlan(j, cleanReason(ctx, red, failed))
	case failed != "":
		plan = failedPlan(j, cleanReason(ctx, red, failed), logWithheld)
	case hit != "":
		plan = needsHumanPlan(j, rf.Status, hit)
	default:
		plan = Build(j, rf, d.Roles)
		if logWithheld {
			plan = needsHumanPlan(j, rf.Status, "")
		}
		plan.Summary = summary
		if roleSeen {
			plan.RolePath = rolePath
		}
	}
	if logWithheld {
		plan.LogWithheld = true
		if plan.Reason != "" {
			plan.Reason += "; "
		}
		plan.Reason += "log withheld: " + logReason
	}
	if logRef != nil {
		plan.LogPath = logRef.Path
	}
	if plan.Outcome == OutcomeFailed || plan.Outcome == OutcomeRetry {
		// The evidence stays for 24 h; the comment says until when and shows
		// the log tail (already redacted, re-scanned and capped).
		plan.KeepUntil = d.now().Add(failedKeep)
		if j.LogPath != "" {
			plan.Tail, plan.TailWithheld = tail, tail == ""
		}
	}
	sort.SliceStable(files, func(a, b int) bool { return files[a].Path < files[b].Path })
	plan.Files = files
	return Record{Version: recordVersion, Phase: PhasePrepared, Job: j, Plan: plan}, nil
}

func verdictWord(err error) string {
	if err != nil {
		return "could not run on"
	}
	return "flagged"
}

func ruleList(fs []redact.Finding) string {
	seen := map[string]bool{}
	var ids []string
	for _, f := range fs {
		if !seen[f.Rule] {
			seen[f.Rule] = true
			ids = append(ids, f.Rule)
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// readResult reads and validates the role file. A bad result is a failure
// reason, not an error; only an unexpected I/O problem is an error.
func readResult(j Job, rolePath string, log *slog.Logger) (RoleFile, string, error) {
	b, err := readRoleFile(filepath.Join(j.Worktree, filepath.FromSlash(rolePath)))
	switch {
	case errors.Is(err, ErrMissing):
		return RoleFile{}, "role file missing", nil
	case errors.Is(err, ErrMalformed):
		return RoleFile{}, "malformed role file: " + strings.TrimPrefix(err.Error(), ErrMalformed.Error()+": "), nil
	case err != nil:
		return RoleFile{}, "", fmt.Errorf("closeout job %s: %w", j.ID, err)
	}
	rf, err := Parse(b)
	if err == nil {
		err = rf.check(j)
	}
	if err != nil {
		return RoleFile{}, "malformed role file: " + strings.TrimPrefix(err.Error(), ErrMalformed.Error()+": "), nil
	}
	if rf.Attempt != j.Attempt {
		// A mismatch is worth a log line but never fails the job.
		log.Warn("closeout.attempt_mismatch", "job", j.ID, "file", rf.Attempt, "job_attempt", j.Attempt)
	}
	return rf, "", nil
}

type candidate struct {
	path   string
	data   []byte
	delete bool
}

// collect lists the changed files under dir and reads them.
func collect(ctx context.Context, j Job, d Deps, dir string) ([]candidate, string, error) {
	ch, err := d.Changed(ctx, j.Worktree, j.BaseSHA, dir)
	if err != nil {
		return nil, "", fmt.Errorf("closeout job %s: list changed files: %w", j.ID, err)
	}
	var out []candidate
	logs := dir + "logs/"
	for _, c := range ch {
		if !strings.HasPrefix(c.Path, dir) || strings.HasPrefix(c.Path, logs) || strings.Contains(c.Path, "..") {
			continue
		}
		if c.Deleted {
			out = append(out, candidate{path: c.Path, delete: true})
			continue
		}
		full := filepath.Join(j.Worktree, filepath.FromSlash(c.Path))
		fi, err := os.Lstat(full)
		switch {
		case err != nil:
			return nil, "", fmt.Errorf("closeout job %s: stat %s: %w", j.ID, c.Path, err)
		case !fi.Mode().IsRegular():
			return nil, "changed file " + c.Path + " is not a regular file", nil
		case fi.Size() > maxFileBytes:
			return nil, "changed file " + c.Path + " is larger than 1 MiB", nil
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, "", fmt.Errorf("closeout job %s: read %s: %w", j.ID, c.Path, err)
		}
		if b == nil {
			b = []byte{}
		}
		out = append(out, candidate{path: c.Path, data: b})
	}
	if len(out) > maxFiles {
		return nil, fmt.Sprintf("%d changed files, max %d", len(out), maxFiles), nil
	}
	return out, "", nil
}

// prepareLog redacts the host log. It returns nil (and a reason) when the log
// must stay host-local. The second result is the comment tail: the last lines
// of the redacted log, or "" when it is withheld or fails a second scan.
func prepareLog(ctx context.Context, red Redactor, j Job, issue int, stage string) (*FileRef, string, string, error) {
	cp, err := redact.CommitPath(issue, j.LogPath)
	if err != nil {
		return nil, "", "log name is not committable", nil
	}
	dst := filepath.Join(stage, ".redacted.log")
	res, err := red.Log(ctx, j.LogPath, dst)
	_ = os.Remove(dst) // the capped Clean bytes are what get committed
	switch {
	case err != nil:
		return nil, "", "redaction could not complete", nil
	case res.Flagged:
		return nil, "", "secret scan flagged it (rules: " + ruleList(res.Findings) + ")", nil
	}
	ref, err := stageFile(stage, cp, res.Clean)
	if err != nil {
		return nil, "", "", fmt.Errorf("closeout job %s: %w", j.ID, err)
	}
	return &ref, cleanTail(ctx, red, res.Clean), "", nil
}

const (
	tailLines    = 50
	tailLineRune = 300
	tailMaxBytes = 8 << 10
)

// cleanTail cuts the last lines of an already redacted log and scans the cut
// again. It fails closed: an error or a hit gives no tail.
func cleanTail(ctx context.Context, red Redactor, clean []byte) string {
	t := lastLines(clean, tailLines, tailLineRune, tailMaxBytes)
	if t == "" {
		return ""
	}
	res, err := red.Text(ctx, []byte(t))
	if err != nil || res.Flagged {
		return ""
	}
	return string(res.Clean)
}

// lastLines returns the last n lines of b, each cut to maxRunes runes, at most
// maxBytes in all (whole lines are dropped from the front first).
func lastLines(b []byte, n, maxRunes, maxBytes int) string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		l = strings.ToValidUTF8(l, "?")
		if r := []rune(l); len(r) > maxRunes {
			l = string(r[:maxRunes-1]) + "…"
		}
		lines[i] = l
	}
	for len(lines) > 0 && len(strings.Join(lines, "\n")) > maxBytes {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

// stageFile writes cleaned bytes under stage at the repo-relative path.
func stageFile(stage, rel string, data []byte) (FileRef, error) {
	p := filepath.Join(stage, "files", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return FileRef{}, fmt.Errorf("stage %s: %w", rel, err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return FileRef{}, fmt.Errorf("stage %s: %w", rel, err)
	}
	return FileRef{Path: rel, Staged: p}, nil
}

// cleanReason redacts a reason that may quote the agent's text.
func cleanReason(ctx context.Context, red Redactor, s string) string {
	res, err := red.Text(ctx, []byte(s))
	if err != nil || res.Flagged {
		return "result withheld by the secret scan"
	}
	return string(res.Clean)
}
