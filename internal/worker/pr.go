package worker

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/github"
)

const (
	maxQuoteRunes     = 4000
	summaryWithheld   = "Summary withheld by the secret scan."
	summaryNone       = "The agent wrote no summary."
	testsRanOutOfTime = "ran out of time"
)

// Title is the PR title: the first line of the code commit message.
func Title(typ, issueTitle string, issue int, jobID string) (string, error) {
	msg, err := committer.CodeMessage(typ, issueTitle, issue, "", jobID)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(msg, "\n")
	return first, nil
}

// QuoteSummary renders agent text for a PR body: closing references
// neutralised (including the HTML-entity and backslash forms of "#"), ard
// markers and @-mentions defused, cut to 4,000 runes and quoted. The text must
// already be redacted.
func QuoteSummary(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if s == "" {
		return "> " + summaryNone
	}
	s = closeout.Sanitize(committer.NeutraliseRefs(s))
	if r := []rune(s); len(r) > maxQuoteRunes {
		s = strings.TrimRight(string(r[:maxQuoteRunes]), "\n") + "\n… (truncated; full text in the committed file)"
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// BodyInfo is what the PR body is made of.
type BodyInfo struct {
	Repo    github.Repo
	Issue   int
	Role    string
	Branch  string
	JobID   string
	Summary string // redacted agent text; "" when withheld or absent
	// Withheld says the secret scan held the summary back.
	Withheld bool
	Result   closeout.WorkerResult
}

// Body renders the PR body. The only closing reference in it is ard's own
// "Closes #<issue>", written here and never passed through NeutraliseRefs.
func Body(b BodyInfo) string {
	var w strings.Builder
	fmt.Fprintf(&w, "Closes #%d\n\n", b.Issue)
	plan := fmt.Sprintf("https://github.com/%s/%s/blob/main/.ard/%d/plan.md", b.Repo.Owner, b.Repo.Name, b.Issue)
	fmt.Fprintf(&w, "Opened by ard from issue #%d. The summary below was written by an agent and is untrusted; the [plan](%s) came from triage.\n\n", b.Issue, plan)
	if b.Withheld {
		w.WriteString("> " + summaryWithheld + "\n")
	} else {
		w.WriteString(QuoteSummary(b.Summary) + "\n")
	}
	fmt.Fprintf(&w, "\n**Tests:** %s\n", testsText(b.Result))
	if b.Result.Large != "" {
		fmt.Fprintf(&w, "\n**%s**\n", b.Result.Large)
	}
	base := fmt.Sprintf("https://github.com/%s/%s", b.Repo.Owner, b.Repo.Name)
	role := fmt.Sprintf(".ard/%d/worker-%s.md", b.Issue, b.Role)
	fmt.Fprintf(&w, "\n**Files:** [%s](%s/blob/%s/%s) · [logs](%s/tree/%s/.ard/%d/logs) · job `%s`\n",
		role, base, b.Branch, role, base, b.Branch, b.Issue, b.JobID)
	return w.String()
}

func testsText(r closeout.WorkerResult) string {
	fixes := ""
	switch r.Fixes {
	case 0:
	case 1:
		fixes = " after 1 fix attempt"
	default:
		fixes = fmt.Sprintf(" after %d fix attempts", r.Fixes)
	}
	switch r.Tests {
	case closeout.TestsPassed:
		return "passed" + fixes
	case closeout.TestsFailed:
		s := "failed" + fixes
		if r.TestNote != "" {
			s += " (" + r.TestNote + ")"
		}
		return s
	}
	return "not configured"
}
