package closeout

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
)

const (
	maxInlineRunes   = 2000
	maxBulletRunes   = 300
	maxReviewRunes   = 60000
	reviewFooterRoom = 2000 // runes kept free for the closing lines
)

// Review modes: how the review reached GitHub.
const (
	ReviewInline  = "inline"  // one PR review with inline comments
	ReviewBody    = "body"    // a PR review with every finding in its body
	ReviewComment = "comment" // a plain PR comment (GitHub took no review)
	ReviewFound   = "found"   // an earlier post was found; nothing was posted
)

// ReviewContext is what the reviewer loop knows that the role file cannot say.
// Added is used by Prepare only and is not persisted: the plan keeps the
// decided placement.
type ReviewContext struct {
	// MergeBase is the commit the diff was taken against.
	MergeBase string
	// Added is, per path, the head-side line ranges the PR added.
	Added map[string][]gitx.LineRange `json:"-"`
	// HeadMoved is set when the PR gained code (anything outside .ard/) after
	// the commit the review pinned. An approve is then not a pass: the review
	// is posted but no label says done, so the request stays and runs again.
	HeadMoved bool
	// HasAid is set when the role has an aid script. Only then do AidFailed
	// and Injected apply.
	HasAid bool `json:",omitempty"`
	// AidFailed is why the aid failed, timed out or produced invalid output;
	// "" when it ran cleanly. A review whose aid failed cannot approve.
	AidFailed string `json:",omitempty"`
	// Injected holds findings ard adds in code (a gitleaks hit on an added
	// line). The agent cannot drop them, and an approve that leaves them out
	// becomes changes.
	Injected []Finding `json:",omitempty"`
}

// aidAdjust applies the aid rules to a review: the findings ard injected are
// added to fs unless the agent already reported that line, and an approve
// becomes changes when the aid failed or a finding was injected. It returns
// the findings, the verdict and a note ("" when nothing changed).
func aidAdjust(rc *ReviewContext, verdict string, fs []Finding) ([]Finding, string, string) {
	if rc == nil || !rc.HasAid {
		return fs, verdict, ""
	}
	for _, in := range rc.Injected {
		dup := false
		for _, f := range fs {
			if f.File == in.File && f.Line == in.Line && strings.HasPrefix(strings.TrimSpace(f.Message), "[secret]") {
				dup = true
				break
			}
		}
		if !dup {
			fs = append(fs, in)
		}
	}
	note := ""
	if verdict == VerdictApprove {
		switch {
		case len(rc.Injected) > 0:
			verdict, note = VerdictChanges, "ard found a secret in the added lines with a pinned tool, so this review cannot approve."
		case rc.AidFailed != "":
			verdict, note = VerdictChanges, "The pre-review aid did not complete ("+rc.AidFailed+"), so this review cannot approve."
		}
	} else if rc.AidFailed != "" && verdict == VerdictChanges {
		note = "The pre-review aid did not complete (" + rc.AidFailed + ")."
	}
	return fs, verdict, note
}

// ReviewPlan is the PR review close-out posts for a review job that reached a
// verdict.
type ReviewPlan struct {
	CommitSHA string // the pinned commit the agent reviewed
	Verdict   string // after any downgrade
	// Downgraded: the role file said approve but carried a blocking finding.
	Downgraded bool
	// AidNote says why ard changed the verdict or what the aid did, "" if nothing.
	AidNote   string `json:",omitempty"`
	HeadMoved bool
	Inline    []Finding // on lines the PR added: posted as inline comments
	Other     []Finding // everything else: listed in the review body
	// NoLines: the added lines were not known (a Reaper close-out after a
	// restart), so every finding is in Other.
	NoLines bool
	// PRGone is set (merged, closed or branch gone) when the PR was gone at
	// close-out: nothing was committed and the job is stale.
	PRGone string `json:",omitempty"`
}

// reviewPlan splits the findings of a review by whether their line is one the
// PR added.
func reviewPlan(j Job, verdict string, downgraded bool, fs []Finding) *ReviewPlan {
	rp := &ReviewPlan{CommitSHA: j.BaseSHA, Verdict: verdict, Downgraded: downgraded}
	if j.Review == nil {
		rp.NoLines, rp.HeadMoved = true, true
		rp.Other = append(rp.Other, fs...)
		return rp
	}
	rp.HeadMoved = j.Review.HeadMoved
	for _, f := range fs {
		if f.Line > 0 && inRanges(f.Line, j.Review.Added[f.File]) {
			rp.Inline = append(rp.Inline, f)
		} else {
			rp.Other = append(rp.Other, f)
		}
	}
	return rp
}

func inRanges(line int, rs []gitx.LineRange) bool {
	for _, r := range rs {
		if line >= r.From && line <= r.To {
			return true
		}
	}
	return false
}

func findingTag(f Finding) string {
	b := "non-blocking"
	if f.Blocking {
		b = "blocking"
	}
	return fmt.Sprintf("**%s · %s**", f.Severity, b)
}

// inlineBody is the text of one inline comment.
func inlineBody(f Finding) string {
	return findingTag(f) + "\n\n" + sanitize(cutRunes(f.Message, maxInlineRunes))
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func bullet(f Finding) string {
	loc := f.File
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(f.Message), "\n", 2)[0])
	return fmt.Sprintf("- %s %s %s", findingTag(f), codeSpan(loc, 340), sanitize(cutRunes(first, maxBulletRunes)))
}

// inlineComments builds the inline comments of a review plan.
func inlineComments(rp *ReviewPlan) []github.InlineComment {
	out := make([]github.InlineComment, 0, len(rp.Inline))
	for _, f := range rp.Inline {
		out = append(out, github.InlineComment{Path: f.File, Line: f.Line, Side: "RIGHT", Body: inlineBody(f)})
	}
	return out
}

// RenderReview builds the body of the PR review (or of the plain comment that
// replaces it). With allInBody every finding is listed in the body, for the
// fallbacks that carry no inline comments. The body is at most 60,000 runes.
func RenderReview(r Record, repo github.Repo, dryRun, allInBody bool) string {
	j, p := r.Job, r.Plan
	rp := p.Review
	var b strings.Builder
	b.WriteString(marker(j.ID, j.Attempt) + "\n")
	fmt.Fprintf(&b, "**ard · review:%s · %s**\n", j.Role, rp.Verdict)
	if s := strings.TrimSpace(p.Summary); s != "" {
		b.WriteString("\n" + sanitize(cutSummary(s)) + "\n")
	}
	var notes []string
	if rp.Downgraded {
		notes = append(notes, "The review approved but listed a blocking finding, so ard treats it as changes requested.")
	}
	if rp.AidNote != "" {
		notes = append(notes, sanitize(cutRunes(rp.AidNote, 400)))
	}
	if rp.HeadMoved && rp.PRGone == "" {
		notes = append(notes, "The branch gained code after this commit was reviewed. The review stays requested and runs again on the new head.")
	}
	if rp.PRGone != "" {
		notes = append(notes, "The pull request was merged or closed before this review finished, so nothing was committed and the job is recorded as stale.")
	}
	if len(notes) > 0 {
		b.WriteString("\n" + strings.Join(notes, "\n") + "\n")
	}
	list, heading := rp.Other, "Findings outside the changed lines:"
	switch {
	case allInBody:
		list = append(append([]Finding(nil), rp.Inline...), rp.Other...)
		heading = "Findings:"
	case rp.NoLines:
		heading = "Findings (line placement unavailable):"
	}
	if len(list) > 0 {
		b.WriteString("\n" + heading + "\n")
		used := len([]rune(b.String()))
		for i, f := range list {
			line := bullet(f)
			if used+len([]rune(line))+1 > maxReviewRunes-reviewFooterRoom {
				fmt.Fprintf(&b, "- … and %d more in the committed file\n", len(list)-i)
				break
			}
			b.WriteString(line + "\n")
			used += len([]rune(line)) + 1
		}
	}
	var lines []string
	if sha := rp.CommitSHA; sha != "" {
		if len(sha) > 12 {
			sha = sha[:12]
		}
		lines = append(lines, fmt.Sprintf("Reviewed commit `%s`", sha))
	}
	if out := outputLine(r, repo, dryRun); out != "" {
		lines = append(lines, out)
	}
	if len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
	return b.String()
}
