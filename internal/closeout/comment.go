package closeout

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
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
	default:
		return string(p.Outcome)
	}
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
	if len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
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
