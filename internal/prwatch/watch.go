package prwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/prgate"
	"github.com/albertcbrown/ard/internal/state"
)

// Transitions, as recorded in pr_watch events and comment markers.
const (
	trClosed  = "closed_unmerged"
	trPending = "pending_timeout"
	trRed     = "draft_red"
)

const maxNameRunes = 80

// sanitizeName makes a CI-controlled check name safe to print in a code span:
// no backticks (they would end the span), no control characters or newlines,
// no @ (a mention), and a length cap.
func sanitizeName(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch {
		case r == '`' || r == '@' || r == '<' || r == '>':
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) && r != ' ':
			r = ' '
		}
		if n == maxNameRunes {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	if out := strings.TrimSpace(b.String()); out != "" {
		return out
	}
	return "?"
}

// codeList renders check names as a comma-separated list of code spans.
func codeList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + sanitizeName(n) + "`"
	}
	return strings.Join(q, ", ")
}

func marker(transition string, pr int, head string) string {
	return fmt.Sprintf("<!-- ard:prwatch %s pr=%d head=%s -->", transition, pr, head)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// watch handles one of ard's PRs.
func (t *tick) watch(ctx context.Context, pr github.PullRequest) {
	l := t.l
	role, issue, _ := prgate.ParseWorkerBranch(pr.HeadRef)
	if t.busy[issue] {
		t.log.Debug("prwatch.skip", "pr", pr.Number, "reason", "job_running")
		return
	}
	row, have, err := l.getRow(ctx, t.dry, pr.Number)
	if err != nil {
		t.fail(pr.Number, "read_row", err)
		return
	}
	open := pr.State == github.StateOpen && !pr.Merged
	switch {
	case !have && !open:
		return // closed before this loop saw it open: history, not news
	case !have:
		row = state.PRWatch{PR: pr.Number, Issue: issue, Head: pr.HeadSHA, HeadSeenAt: t.now}
	case open && row.Head != pr.HeadSHA:
		t.log.Debug("prwatch.head_moved", "pr", pr.Number, "from", short(row.Head), "to", short(pr.HeadSHA))
		row.Head, row.HeadSeenAt, row.ChecksState = pr.HeadSHA, t.now, ""
	}
	switch {
	case pr.Merged:
		t.merged(ctx, pr, issue, &row)
	case !open:
		t.closed(ctx, pr, issue, role, &row)
	default:
		t.open(ctx, pr, issue, &row)
	}
	if err := l.putRow(ctx, t.dry, row); err != nil {
		t.fail(pr.Number, "write_row", err)
	}
}

// open judges the checks of an open PR and sends the notices that are due.
func (t *tick) open(ctx context.Context, pr github.PullRequest, issue int, row *state.PRWatch) {
	head := pr.HeadSHA
	// A settled head costs nothing, unless a draft's red names are still owed.
	cached := row.ChecksState == "green" || row.ChecksState == "red"
	owes := row.ChecksState == "red" && pr.Draft && row.RedNotifiedHead != head
	var names []string
	st := row.ChecksState
	if !cached || owes {
		var err error
		if st, names, err = t.evaluate(ctx, head); err != nil {
			t.fail(pr.Number, "checks", err)
			return
		}
		row.ChecksState = st
	}
	switch {
	case st == prgate.ChecksPending.String():
		if t.now.Sub(row.HeadSeenAt) > t.timeout && row.PendingNotifiedHead != head {
			t.pendingNotice(ctx, pr, issue, row, names)
		}
	case st == prgate.ChecksRed.String() && pr.Draft && row.RedNotifiedHead != head:
		t.draftRed(ctx, pr, issue, row, names)
	case st == prgate.ChecksRed.String():
		t.log.Debug("prwatch.ready_red", "pr", pr.Number, "hint", "the merger handles red checks on a ready PR")
	}
}

// evaluate reads the checks of head and judges them with the merger's rule.
// graceOver is true: only required-check pending time matters here.
func (t *tick) evaluate(ctx context.Context, head string) (string, []string, error) {
	d := t.l.d
	required, err := t.requiredChecks(ctx)
	if err != nil {
		return "", nil, err
	}
	runs, err := d.Reader.ListCheckRuns(ctx, head)
	if err != nil {
		return "", nil, fmt.Errorf("check runs: %w", err)
	}
	statuses, err := d.Reader.ListCommitStatuses(ctx, head)
	if err != nil {
		return "", nil, fmt.Errorf("commit statuses: %w", err)
	}
	if err := prgate.ResolveApps(ctx, required, statuses, t.l.appID); err != nil {
		return "", nil, err
	}
	st, names := prgate.EvalChecks(required, runs, statuses, true)
	return st.String(), names, nil
}

func (t *tick) pendingNotice(ctx context.Context, pr github.PullRequest, issue int, row *state.PRWatch, names []string) {
	status := t.l.d.StatusIssue()
	if status == 0 {
		t.log.Info("prwatch.no_status_issue", "pr", pr.Number, "hint", "the pending-checks notice waits for the status issue")
		return
	}
	head := pr.HeadSHA
	waiting := ""
	if len(names) > 0 {
		waiting = ": " + codeList(names)
	}
	t.log.Info("prwatch.pending_timeout", "pr", pr.Number, "head", short(head), "waiting", len(names))
	body := fmt.Sprintf("**ard · PR watch**\n\nPR #%d (issue #%d) has had required checks pending on `%s` for more than %s%s.\n\n"+
		"Check the CI for this commit. ard does not act on this; a new commit restarts the clock.\n\n%s",
		pr.Number, issue, short(head), config.Duration(t.timeout), waiting, marker(trPending, pr.Number, head))
	if t.once(ctx, pr.Number, issue, head, trPending, status, row.HeadSeenAt, func() error {
		_, err := t.l.d.Effects.CreateComment(ctx, status, body)
		return err
	}) {
		row.PendingNotifiedHead = head
	}
}

func (t *tick) draftRed(ctx context.Context, pr github.PullRequest, issue int, row *state.PRWatch, names []string) {
	head := pr.HeadSHA
	t.log.Info("prwatch.draft_red", "pr", pr.Number, "head", short(head), "failed", len(names))
	body := fmt.Sprintf("**ard · PR watch**\n\nRequired checks failed on `%s`: %s.\n\n"+
		"ard does not act on a draft PR. Push a fix, or close the PR.\n\n%s",
		short(head), codeList(names), marker(trRed, pr.Number, head))
	if t.once(ctx, pr.Number, issue, head, trRed, pr.Number, time.Time{}, func() error {
		_, err := t.l.d.Effects.CreateComment(ctx, pr.Number, body)
		return err
	}) {
		row.RedNotifiedHead = head
	}
}

// closed handles a PR closed without merging: its issue is stranded with the
// worker :done label unless it is closed or a merge is recorded.
func (t *tick) closed(ctx context.Context, pr github.PullRequest, issue int, role string, row *state.PRWatch) {
	if row.ClosedNotified {
		return
	}
	d := t.l.d
	is, err := d.Reader.GetIssue(ctx, issue)
	if err != nil {
		t.fail(pr.Number, "get_issue", err)
		return
	}
	if is.State != github.StateOpen || is.IsPR {
		t.log.Debug("prwatch.closed_issue_closed", "pr", pr.Number, "issue", issue)
		row.ClosedNotified = true
		return
	}
	merges, err := d.Store.Events(ctx, state.EventFilter{Kind: state.EventMerge, Issue: issue})
	if err != nil {
		t.fail(pr.Number, "events", err)
		return
	}
	if len(merges) > 0 {
		t.log.Debug("prwatch.closed_but_merged", "pr", pr.Number, "issue", issue)
		row.ClosedNotified = true
		return
	}
	for _, other := range t.prs {
		if other.Number != pr.Number && other.State == github.StateOpen && !other.Merged {
			if _, n, _ := prgate.ParseWorkerBranch(other.HeadRef); n == issue {
				t.log.Debug("prwatch.closed_other_pr_open", "pr", pr.Number, "issue", issue, "other", other.Number)
				row.ClosedNotified = true
				return
			}
		}
	}
	head := pr.HeadSHA
	t.log.Info("prwatch.closed_unmerged", "pr", pr.Number, "issue", issue)
	body := fmt.Sprintf("**ard · PR watch**\n\nPR #%d was closed without merging, so this issue has no pull request and ard has stopped work on it (`%s` added).\n\n"+
		"To try again, remove the `%s` label and ard runs the worker again. Or close this issue.\n\n%s",
		pr.Number, labels.NeedsHuman, labels.Done(labels.StageWorker, role), marker(trClosed, pr.Number, head))
	if t.once(ctx, pr.Number, issue, head, trClosed, issue, time.Time{}, func() error {
		if err := d.Effects.AddLabels(ctx, issue, labels.NeedsHuman); err != nil {
			return err
		}
		_, err := d.Effects.CreateComment(ctx, issue, body)
		return err
	}) {
		row.ClosedNotified = true
	}
}

// merged records a merge ard did not make. ard's own merges already have a
// merge event; a merge by the App without one is marked seen, not recorded.
func (t *tick) merged(ctx context.Context, pr github.PullRequest, issue int, row *state.PRWatch) {
	if row.MergeRecorded {
		return
	}
	d := t.l.d
	evs, err := d.Store.Events(ctx, state.EventFilter{Kind: state.EventMerge, Issue: issue})
	if err != nil {
		t.fail(pr.Number, "events", err)
		return
	}
	for _, e := range evs {
		var data struct {
			PR int `json:"pr"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.PR == pr.Number {
			row.MergeRecorded = true
			return
		}
	}
	full, err := d.Reader.GetPR(ctx, pr.Number)
	if err != nil {
		t.fail(pr.Number, "get_pr", err)
		return
	}
	switch {
	case full.MergedBy == "":
		t.log.Warn("prwatch.merger_unknown", "pr", pr.Number)
		return
	case strings.EqualFold(full.MergedBy, t.self):
		t.log.Debug("prwatch.merged_by_ard", "pr", pr.Number)
	case t.dry:
		t.logOnce(fmt.Sprintf("merge/%d", pr.Number), "prwatch.would_record_human_merge", "pr", pr.Number, "issue", issue, "actor", full.MergedBy)
	default:
		data, _ := json.Marshal(map[string]any{"pr": pr.Number, "head": pr.HeadSHA, "actor": full.MergedBy})
		if err := d.Store.AppendEvent(ctx, state.Event{At: t.now, Kind: state.EventMerge, Issue: issue, Data: data}); err != nil {
			t.fail(pr.Number, "record_merge", err)
			return
		}
		t.log.Info("prwatch.human_merge", "pr", pr.Number, "issue", issue, "actor", full.MergedBy)
	}
	row.MergeRecorded = true
}

// once runs effect at most once per (pr, head, transition), across ticks and
// restarts: an event, or the marker comment on target (read since since),
// shows it was done. It returns true when the transition is recorded.
func (t *tick) once(ctx context.Context, pr, issue int, head, transition string, target int, since time.Time, effect func() error) bool {
	d := t.l.d
	evs, err := d.Store.Events(ctx, state.EventFilter{Kind: state.EventPRWatch, Issue: issue})
	if err != nil {
		t.fail(pr, "events", err)
		return false
	}
	for _, e := range evs {
		var data struct {
			PR         int    `json:"pr"`
			Head       string `json:"head"`
			Transition string `json:"transition"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.PR == pr && data.Head == head && data.Transition == transition {
			return true
		}
	}
	comments, err := d.Reader.ListComments(ctx, target, since)
	if err != nil {
		t.fail(pr, "comments", err)
		return false
	}
	m := marker(transition, pr, head)
	found := false
	for _, c := range comments {
		if strings.Contains(c.Body, m) {
			found = true
			break
		}
	}
	if !found {
		if err := effect(); err != nil {
			t.fail(pr, transition, err)
			return false
		}
	}
	if !t.dry {
		data, _ := json.Marshal(map[string]any{"pr": pr, "head": head, "transition": transition})
		if err := d.Store.AppendEvent(ctx, state.Event{At: t.now, Kind: state.EventPRWatch, Issue: issue, Data: data}); err != nil {
			t.log.Warn("prwatch.event_failed", "pr", pr, "transition", transition, "error", err)
		}
	}
	return true
}
