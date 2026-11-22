package closeout

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

const (
	maxSummaryRunes = 4000
	maxReasonRunes  = 300
	markerPrefix    = "<!-- ard:closeout job="
)

// marker is the hidden first line of the close-out comment. Resume looks for
// it so a replay never posts a second comment.
func marker(jobID string, attempt int) string {
	return fmt.Sprintf("%s%s attempt=%d -->", markerPrefix, jobID, attempt)
}

func markerFor(jobID string) string { return markerPrefix + jobID + " " }

// sanitize makes agent text safe to post: it cannot forge an ard marker
// (<!-- ard:closeout, ard:status, ard:fp) and cannot ping anyone.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "<!--", "&lt;!--")
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		b.WriteRune(r)
		if r == '@' && i+1 < len(rs) && isAlnum(rs[i+1]) {
			b.WriteRune('​')
		}
	}
	return b.String()
}

func isAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// codeSpan renders s on one line inside backticks, cut to max runes.
func codeSpan(s string, max int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "'")), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return "`" + sanitize(s) + "`"
}

// cutSummary cuts s to maxSummaryRunes at a line end.
func cutSummary(s string) string {
	r := []rune(s)
	if len(r) <= maxSummaryRunes {
		return s
	}
	cut := string(r[:maxSummaryRunes])
	if i := strings.LastIndex(cut, "\n"); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, "\n") + "\n… (truncated; full text in the committed file)"
}

func headerWord(p Plan) string {
	switch p.Outcome {
	case OutcomeNeedsHuman:
		if p.Status == StatusBlocked {
			return "blocked"
		}
		return "needs-human"
	case OutcomeRetry:
		return "retrying"
	case OutcomeLimited:
		return "usage limit"
	default:
		return string(p.Outcome)
	}
}

// fenceFor returns a code fence one backtick longer than the longest backtick
// run in s, and at least three, so s cannot close it.
func fenceFor(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// evidenceLines are the extra lines of a failed or retry comment: what will
// happen next, the log tail and how long the evidence is kept. Host paths are
// never printed; they would reveal the user's home directory.
func evidenceLines(r Record) (before []string, tail string) {
	p := r.Plan
	if p.Outcome != OutcomeFailed && p.Outcome != OutcomeRetry && p.Outcome != OutcomeLimited {
		return nil, ""
	}
	if p.Outcome == OutcomeRetry {
		before = append(before, "Retrying once as a new job.")
	}
	if p.Outcome == OutcomeLimited {
		before = append(before, limitLine(r.Job.Cause))
	}
	switch {
	case p.Tail != "":
		s := sanitize(p.Tail)
		f := fenceFor(s)
		tail = "Last 50 log lines:\n" + f + "\n" + s + "\n" + f
	case p.TailWithheld:
		tail = "Log tail withheld."
	}
	if !p.KeepUntil.IsZero() {
		before = append(before, fmt.Sprintf("Kept until %s (worktree and host files of job `%s`); add `%s` to hold them longer.",
			p.KeepUntil.UTC().Format("2006-01-02 15:04 UTC"), r.Job.ID, labels.Keep))
	}
	return before, tail
}

// limitLine says when a usage-limit stop re-runs. The family is a fixed word
// (opus, sonnet) or "account"; nothing the CLI printed is echoed.
func limitLine(c Cause) string {
	who := "account"
	if c.Family != "" {
		who = c.Family
	}
	if c.ResetsAt.IsZero() {
		return "Paused for usage limit; re-runs after the reset (" + sanitize(who) + ")."
	}
	return fmt.Sprintf("Paused for usage limit until %s (%s); re-runs after the reset.",
		c.ResetsAt.UTC().Format("2006-01-02 15:04 UTC"), sanitize(who))
}

// RenderComment builds the close-out comment for a record. In dry-run, or
// when nothing was committed, output paths are plain text with no link.
func RenderComment(r Record, repo github.Repo, dryRun bool) string {
	j, p := r.Job, r.Plan
	var b strings.Builder
	b.WriteString(marker(j.ID, j.Attempt) + "\n")
	fmt.Fprintf(&b, "**ard · %s:%s · %s**\n", j.Stage, j.Role, headerWord(p))
	if s := strings.TrimSpace(p.Summary); s != "" {
		b.WriteString("\n" + sanitize(cutSummary(s)) + "\n")
	}
	if p.Reason != "" {
		b.WriteString("\n" + codeSpan(p.Reason, maxReasonRunes) + "\n")
	}
	var lines []string
	if len(p.Requests) > 0 {
		names := make([]string, len(p.Requests))
		for i, l := range p.Requests {
			names[i] = "`" + l.Name + "`"
		}
		lines = append(lines, "Requested next: "+strings.Join(names, ", "))
	}
	if len(p.Deferred) > 0 {
		names := make([]string, len(p.Deferred))
		for i, d := range p.Deferred {
			names[i] = "`" + d.String() + "`"
		}
		lines = append(lines, "Deferred until the PR exists: "+strings.Join(names, ", "))
	}
	if len(p.Skipped) > 0 {
		parts := make([]string, len(p.Skipped))
		for i, s := range p.Skipped {
			parts[i] = codeSpan(s.Entry, 60) + " (" + sanitize(s.Why) + ")"
		}
		lines = append(lines, "Skipped: "+strings.Join(parts, ", "))
	}
	if out := outputLine(r, repo, dryRun); out != "" {
		lines = append(lines, out)
	}
	extra, tail := evidenceLines(r)
	lines = append(lines, extra...)
	if len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
	if tail != "" {
		b.WriteString("\n" + tail + "\n")
	}
	return b.String()
}

func outputLine(r Record, repo github.Repo, dryRun bool) string {
	j, p := r.Job, r.Plan
	link := func(path string) string {
		if dryRun || r.SHA == "" {
			return path
		}
		return fmt.Sprintf("[%s](https://github.com/%s/%s/blob/%s/%s)", path, repo.Owner, repo.Name, r.SHA, path)
	}
	var parts []string
	if p.RolePath != "" {
		parts = append(parts, link(p.RolePath))
	}
	switch {
	case p.LogPath != "":
		parts = append(parts, "log "+link(p.LogPath))
	case p.LogWithheld:
		parts = append(parts, "log withheld (secret scan)")
	}
	if len(parts) == 0 {
		return ""
	}
	parts = append(parts, fmt.Sprintf("job `%s`", j.ID), fmt.Sprintf("attempt %d", j.Attempt))
	return "Output: " + strings.Join(parts, " · ")
}
