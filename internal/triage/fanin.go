package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/query"
	"github.com/albertcbrown/ard/internal/intake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

type fanInVerdict int

const (
	fSkip  fanInVerdict = iota // not the gate's business (yet)
	fWait                      // triage not finished
	fHuman                     // put it to a human: comment + ard:needs-human
	fReady                     // triage finished; caller checks the body hash, then approval
)

func (v fanInVerdict) String() string { return [...]string{"skip", "wait", "human", "ready"}[v] }

// Reasons. Fixed text: never issue text.
const (
	rLeadNotDone    = "lead-not-done"
	rGuarded        = "guarded"
	rWorkerStarted  = "worker-started"
	rLeadRequested  = "lead-requested"
	rTriageRunning  = "triage-running"
	rWaiting        = "waiting"
	rUnknownRole    = "unknown-role"
	rNoWorker       = "no-worker"
	rWorkerGone     = "worker-gone"
	rReady          = "ready"
	rBodyChanged    = "body-changed"
	rApprovalNeeded = "approval-needed"
	rApprovalBad    = "approval-refused"
	rStart          = "start"
)

type fanInInput struct {
	Labels  []string        // the fresh re-read
	Triage  map[string]bool // configured triage role names
	Workers map[string]bool // configured worker role names
	Busy    bool            // a triage job of this issue is running in state
	Worker  string          // the lead's validated choice from LeadWorker; "" = none or no record
}

type fanInResult struct {
	Verdict fanInVerdict
	Reason  string
	Role    string // the triage role a wait or unknown-role names
	Worker  string // fReady and worker-gone only
}

// decideFanIn is the gate. Pure; the first matching check wins.
func decideFanIn(in fanInInput) fanInResult {
	if !hasLabel(in.Labels, labels.Done(labels.StageTriage, leadRole)) {
		return fanInResult{Verdict: fSkip, Reason: rLeadNotDone}
	}
	if hasAny(in.Labels, []string{labels.NeedsHuman, labels.Hold, labels.ExecutorFailed}) {
		return fanInResult{Verdict: fSkip, Reason: rGuarded}
	}
	var requested []string
	running := in.Busy
	for _, l := range in.Labels {
		p, ok := labels.Parse(l)
		if !ok || p.Kind != labels.KindRole {
			continue
		}
		switch {
		case p.Stage == labels.StageWorker:
			// A worker already gated, started by a human, or in a fix round.
			return fanInResult{Verdict: fSkip, Reason: rWorkerStarted}
		case p.Stage == labels.StageTriage && p.State == labels.StateRequest:
			requested = append(requested, p.Role)
		case p.Stage == labels.StageTriage && p.State == labels.StateRunning:
			running = true
		}
	}
	if hasLabel(in.Labels, labels.Pending) {
		return fanInResult{Verdict: fSkip, Reason: rLeadRequested}
	}
	if running {
		return fanInResult{Verdict: fWait, Reason: rTriageRunning}
	}
	sort.Strings(requested)
	wait := ""
	for _, r := range requested {
		if !in.Triage[r] {
			return fanInResult{Verdict: fHuman, Reason: rUnknownRole, Role: r}
		}
		if wait == "" && !hasLabel(in.Labels, labels.Done(labels.StageTriage, r)) {
			wait = r
		}
	}
	if wait != "" {
		return fanInResult{Verdict: fWait, Reason: rWaiting, Role: wait}
	}
	if in.Worker == "" {
		return fanInResult{Verdict: fHuman, Reason: rNoWorker}
	}
	if !in.Workers[in.Worker] {
		return fanInResult{Verdict: fHuman, Reason: rWorkerGone, Worker: in.Worker}
	}
	return fanInResult{Verdict: fReady, Reason: rReady, Worker: in.Worker}
}

// approvalEvent returns the labeled event behind the current ard:approved: the
// latest "labeled" for it, reset by a later "unlabeled". Pure.
func approvalEvent(tl []github.TimelineEvent) (github.TimelineEvent, bool) {
	var ev github.TimelineEvent
	found := false
	for _, e := range tl {
		if !strings.EqualFold(e.Label, labels.Approved) {
			continue
		}
		switch e.Event {
		case "labeled":
			ev, found = e, true
		case "unlabeled":
			ev, found = github.TimelineEvent{}, false
		}
	}
	return ev, found
}

// lastTriageDone returns when the latest ard:triage:<role>:done label was
// added. ok is false when the timeline shows none.
func lastTriageDone(tl []github.TimelineEvent) (time.Time, bool) {
	var at time.Time
	ok := false
	for _, e := range tl {
		if e.Event != "labeled" {
			continue
		}
		p, valid := labels.Parse(e.Label)
		if !valid || p.Kind != labels.KindRole || p.Stage != labels.StageTriage || p.State != labels.StateDone {
			continue
		}
		if !ok || e.CreatedAt.After(at) {
			at, ok = e.CreatedAt, true
		}
	}
	return at, ok
}

func names(rs []config.Role) map[string]bool {
	m := make(map[string]bool, len(rs))
	for _, r := range rs {
		m[r.Name] = true
	}
	return m
}

// fanIn is the fan-in gate step of a tick. One search per tick finds issues
// whose lead is done and that no worker has been requested for; each hit is
// re-read and decided. Errors for one issue are joined, never stop the rest.
func (t *tick) fanIn(ctx context.Context) []error {
	l := t.l
	running, err := l.d.Store.RunningJobs(ctx)
	if err != nil {
		return []error{fmt.Errorf("triage gate: list running jobs: %w", err)}
	}
	busy := map[int]bool{}
	for _, j := range running {
		if j.Stage == stageTriage {
			busy[j.Issue] = true
		}
	}
	states := []string{labels.Pending}
	for _, w := range t.cfg.RolesOf(config.KindWorker) {
		states = append(states, labels.Request(labels.StageWorker, w.Name))
	}
	// Gated issues keep ard:triage:lead:done until they close, so the worker
	// request labels are excluded or they would fill the window and starve new ones.
	q, err := query.RoleQuery{
		Type: query.TypeIssue, Request: labels.Done(labels.StageTriage, leadRole), States: states,
		Guards: []string{labels.ExecutorFailed, labels.NeedsHuman, labels.Hold},
	}.Build()
	if err != nil {
		return []error{fmt.Errorf("triage gate: %w", err)}
	}
	hits, err := l.d.Reader.SearchIssues(ctx, q, github.SearchOpts{Sort: "updated", Order: "desc", Max: 100})
	if err != nil {
		return []error{fmt.Errorf("triage gate: search: %w", err)}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Number < hits[j].Number })
	var errs []error
	for _, hit := range hits {
		if err := t.gateIssue(ctx, hit.Number, busy[hit.Number]); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// gateIssue re-reads issue n (search is eventually consistent) and acts on the
// gate's verdict.
func (t *tick) gateIssue(ctx context.Context, n int, busy bool) error {
	l := t.l
	is, err := l.d.Reader.GetIssue(ctx, n)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("triage gate issue %d: re-read: %w", n, err)
	}
	if is.State != github.StateOpen || is.IsPR {
		return nil
	}
	worker, jobID, ok, err := closeout.LeadWorker(ctx, l.d.Store, n)
	if err != nil {
		return fmt.Errorf("triage gate: %w", err)
	}
	if !ok && hasLabel(is.Labels, labels.Done(labels.StageTriage, leadRole)) {
		t.log.Warn("triage.gate_no_record", "issue", n, "hint", "no closed lead close-out in local state")
	}
	// Close-out applies the lead's requests before :done, in one AddLabels
	// call, so when :done is visible every request is visible too.
	res := decideFanIn(fanInInput{
		Labels: is.Labels, Triage: names(t.cfg.RolesOf(config.KindTriage)), Workers: names(t.cfg.RolesOf(config.KindWorker)),
		Busy: busy, Worker: worker,
	})
	switch res.Verdict {
	case fSkip:
		return nil
	case fWait:
		t.log.Debug("triage.gate_wait", "issue", n, "reason", res.Reason, "role", res.Role)
		return nil
	}
	if jobID == "" {
		jobID = "none"
	}
	if t.dry {
		t.log.Info("triage.would_gate", "issue", n, "verdict", res.Verdict.String(), "reason", res.Reason, "worker", res.Worker)
		return nil
	}
	if res.Verdict == fHuman {
		return t.gateHuman(ctx, n, jobID, res)
	}
	return t.gateReady(ctx, is, jobID, res.Worker)
}

const needsHumanHead = "**ard · triage · needs a human**\n\n"

// gateHuman posts one comment, adds ard:needs-human, records the event.
func (t *tick) gateHuman(ctx context.Context, n int, jobID string, res fanInResult) error {
	options := "A maintainer can add `ard:worker:<role>` and remove `ard:needs-human` to start a worker, re-run triage, or close the issue if no change is needed."
	var marker, text string
	switch res.Reason {
	case rNoWorker:
		marker = fmt.Sprintf("<!-- %striage gate=no-worker job=%s -->", labels.Prefix, jobID)
		text = "Triage finished but the lead named no worker role, so ard will not start one. " + options
	case rWorkerGone:
		marker = fmt.Sprintf("<!-- %striage gate=worker-gone worker=%s -->", labels.Prefix, res.Worker)
		text = fmt.Sprintf("The lead chose the `%s` worker, but no such worker role exists now. %s", res.Worker, options)
	default: // rUnknownRole
		marker = fmt.Sprintf("<!-- %striage gate=unknown-role role=%s -->", labels.Prefix, res.Role)
		text = fmt.Sprintf("`ard:triage:%s` is requested but there is no `.ard/triage/%s/` role, so triage can never finish. A maintainer can remove the label and `%s`.",
			res.Role, res.Role, labels.NeedsHuman)
	}
	if err := t.postOnce(ctx, n, marker, needsHumanHead+text); err != nil {
		return err
	}
	if err := t.l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		return fmt.Errorf("triage gate issue %d: add %s: %w", n, labels.NeedsHuman, err)
	}
	t.gateEvent(ctx, n, "human", res.Reason, res.Worker)
	t.log.Info("triage.gate", "issue", n, "verdict", "human", "reason", res.Reason)
	return nil
}

// gateReady runs the checks that need a read of the issue's history, then
// starts the worker.
func (t *tick) gateReady(ctx context.Context, is github.Issue, jobID, worker string) error {
	l := t.l
	n := is.Number
	if !strings.EqualFold(is.Author, t.self) {
		stored, err := l.d.Store.Admission(ctx, n)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				t.log.Info("triage.not_admitted", "issue", n, "role", leadRole)
				return nil
			}
			return fmt.Errorf("triage gate issue %d: read admission: %w", n, err)
		}
		if hash := agentjob.BodyHash(is); stored.BodyHash != hash {
			return t.regateAfterTriage(ctx, is, hash, worker)
		}
	}
	if t.cfg.RequireApproval {
		ok, err := t.approved(ctx, is, jobID, worker)
		if err != nil || !ok {
			return err
		}
	}
	if err := l.d.Effects.AddLabels(ctx, n, labels.Request(labels.StageWorker, worker)); err != nil {
		return fmt.Errorf("triage gate issue %d: add worker label: %w", n, err)
	}
	t.gateEvent(ctx, n, "start", rStart, worker)
	t.log.Info("triage.gate", "issue", n, "verdict", "start", "worker", worker)
	return nil
}

// regateAfterTriage mirrors regate for an issue edited after triage finished.
// It does not offer re-admission: a lead with :done is never re-claimed.
func (t *tick) regateAfterTriage(ctx context.Context, is github.Issue, hash, worker string) error {
	n := is.Number
	if err := t.markRegated(ctx, is, hash); err != nil {
		return err
	}
	marker := fmt.Sprintf("<!-- %striage gate=regate body=%s -->", labels.Prefix, hash[:12])
	text := fmt.Sprintf("**ard · triage · paused**\n\nThe title or body of this issue changed after triage finished, so ard will not start a worker. "+
		"To re-plan, a maintainer removes `%s` and `%s` and re-applies `%s`. To keep the current plan, a maintainer adds `%s` and removes `%s`.",
		labels.Done(labels.StageTriage, leadRole), labels.NeedsHuman, intake.Label(t.cfg.Intake), labels.Request(labels.StageWorker, worker), labels.NeedsHuman)
	if err := t.postOnce(ctx, n, marker, text); err != nil {
		return err
	}
	if err := t.l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		return fmt.Errorf("triage gate issue %d: add %s: %w", n, labels.NeedsHuman, err)
	}
	t.gateEvent(ctx, n, "regate", rBodyChanged, worker)
	return nil
}

// approved reports whether a valid approval is in place. Without ard:approved
// it posts the one-per-lead-job "ready for approval" comment and waits. The
// approval counts only when its latest labeled event is by a collaborator with
// triage permission or higher (not the App) and comes after the last triage
// :done. A stale approval is left in place and waited on; an approval from
// anyone else sends the issue to a human.
func (t *tick) approved(ctx context.Context, is github.Issue, jobID, worker string) (bool, error) {
	l := t.l
	n := is.Number
	if !hasLabel(is.Labels, labels.Approved) {
		return false, t.askApproval(ctx, n, jobID, worker)
	}
	tl, err := l.d.Reader.ListTimeline(ctx, n)
	if err != nil {
		return false, fmt.Errorf("triage gate issue %d: timeline: %w", n, err)
	}
	ev, found := approvalEvent(tl)
	doneAt, haveDone := lastTriageDone(tl)
	if !found || !haveDone {
		t.log.Info("triage.approval_unknown", "issue", n, "reason", "timeline has no matching event yet")
		return false, nil
	}
	for _, lb := range is.Labels {
		p, ok := labels.Parse(lb)
		if !ok || p.Kind != labels.KindRole || p.Stage != labels.StageTriage || p.State != labels.StateDone {
			continue
		}
		seen := false
		for _, e := range tl {
			seen = seen || (e.Event == "labeled" && strings.EqualFold(e.Label, lb))
		}
		if !seen {
			t.log.Info("triage.approval_unknown", "issue", n, "reason", "done label not in timeline yet")
			return false, nil
		}
	}
	refused := ev.Actor == "" || strings.EqualFold(ev.Actor, t.self)
	if !refused {
		p, err := l.d.Reader.GetPermission(ctx, ev.Actor)
		if err != nil {
			return false, fmt.Errorf("triage gate issue %d: permission of approver: %w", n, err)
		}
		if p == (github.Permission{}) {
			t.log.Info("triage.approval_unknown", "issue", n, "reason", "permission unknown")
			return false, nil
		}
		refused = !p.AtLeastTriage()
	}
	if refused {
		marker := fmt.Sprintf("<!-- %striage gate=approval-refused at=%d -->", labels.Prefix, ev.CreatedAt.Unix())
		text := fmt.Sprintf("**ard · triage · needs a human**\n\nThe most recent `%s` on this issue was not added by a maintainer, so ard will not start a worker. "+
			"A maintainer can re-apply `%s` and remove `%s`.", labels.Approved, labels.Approved, labels.NeedsHuman)
		if err := t.postOnce(ctx, n, marker, text); err != nil {
			return false, err
		}
		if err := l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
			return false, fmt.Errorf("triage gate issue %d: add %s: %w", n, labels.NeedsHuman, err)
		}
		t.gateEvent(ctx, n, "approval-refused", rApprovalBad, worker)
		return false, nil
	}
	if !ev.CreatedAt.After(doneAt) {
		t.log.Info("triage.approval_stale", "issue", n)
		return false, t.askApproval(ctx, n, jobID, worker)
	}
	return true, nil
}

// gateEvent records that the gate acted. Failure is logged, not returned, as
// tick.event does.
func (t *tick) gateEvent(ctx context.Context, issue int, verdict, reason, worker string) {
	data, _ := json.Marshal(map[string]string{"verdict": verdict, "reason": reason, "worker": worker})
	e := state.Event{Kind: state.EventGate, Issue: issue, Stage: stageTriage, Role: leadRole, Data: data}
	if err := t.l.d.Store.AppendEvent(ctx, e); err != nil {
		t.log.Warn("triage.event_failed", "issue", issue, "action", "gate", "error", err)
	}
}

// askApproval posts the one-per-lead-job "ready for approval" comment. It
// changes no label and records no event.
func (t *tick) askApproval(ctx context.Context, n int, jobID, worker string) error {
	marker := fmt.Sprintf("<!-- %striage gate=approval job=%s -->", labels.Prefix, jobID)
	text := fmt.Sprintf("**ard · triage · ready for approval**\n\nTriage is complete; the plan is in `.ard/%d/plan.md`. A maintainer adds `%s` to start the `%s` worker. "+
		"An approval given before triage finished does not count: remove and re-add the label.", n, labels.Approved, worker)
	return t.postOnce(ctx, n, marker, text)
}
