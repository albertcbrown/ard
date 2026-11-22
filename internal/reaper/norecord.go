package reaper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// noRecordGrace is how long after a job ended its :running label may still be
// on the issue: close-out removes the label last, after our read.
const noRecordGrace = 10 * time.Minute

const noRecordMarker = "<!-- ard:reaper norecord label="

// noRecord is step 5: a :running label of a configured triage, worker or
// reviewer role that no job of local state accounts for. It happens when the
// state database was lost or a claim crashed between the label and the row.
// With no row there is no close-out record to resume, so this is the one place
// the Reaper writes labels itself. It never runs in dry-run: real labels never
// match the dry-run database.
func (r *Reaper) noRecord(ctx context.Context, cfg *config.Resolved) []error {
	if cfg == nil || r.d.Effects.DryRun() {
		return nil
	}
	running, err := r.d.Store.RunningJobs(ctx)
	if err != nil {
		return []error{fmt.Errorf("reaper: no-record check: list running jobs: %w", err)}
	}
	recent, err := r.d.Store.EndedSince(ctx, r.now().Add(-noRecordGrace))
	if err != nil {
		return []error{fmt.Errorf("reaper: no-record check: list ended jobs: %w", err)}
	}
	type key struct {
		stage, role string
		n           int
	}
	known := map[key]bool{}
	for _, sj := range append(running, recent...) {
		n := sj.Issue
		if sj.Stage == string(labels.StageReview) {
			n = sj.PR
		}
		known[key{sj.Stage, sj.Role, n}] = true
	}
	var errs []error
	for _, k := range []struct {
		kind  config.Kind
		stage labels.Stage
	}{{config.KindTriage, labels.StageTriage}, {config.KindWorker, labels.StageWorker}, {config.KindReviewer, labels.StageReview}} {
		for _, role := range cfg.RolesOf(k.kind) {
			label := labels.Running(k.stage, role.Name)
			issues, err := r.d.Reader.ListIssues(ctx, github.ListIssuesOpts{Labels: []string{label}, IncludePRs: true})
			if err != nil {
				errs = append(errs, fmt.Errorf("reaper: list issues with %s: %w", label, err))
				continue
			}
			for _, is := range issues {
				if hasLabel(is.Labels, labels.Hold) || known[key{string(k.stage), role.Name, is.Number}] {
					continue
				}
				if err := r.failNoRecord(ctx, is.Number, string(k.stage), role.Name, label); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errs
}

// failNoRecord re-reads the issue, then comments once, adds
// ard:executor:failed and removes :running. The marker, which carries the time
// the label was added, makes a replay after a crash safe and keeps an earlier
// incident's comment from suppressing a new one.
func (r *Reaper) failNoRecord(ctx context.Context, n int, stage, role, label string) error {
	fresh, err := r.d.Reader.GetIssue(ctx, n)
	if err != nil {
		return fmt.Errorf("reaper: re-read #%d: %w", n, err)
	}
	if !hasLabel(fresh.Labels, label) || hasLabel(fresh.Labels, labels.Hold) {
		return nil
	}
	// A job claimed after the caller's snapshot has a row now: re-check.
	if busy, err := r.hasJob(ctx, stage, role, n); err != nil || busy {
		return err
	}
	tl, err := r.d.Reader.ListTimeline(ctx, n)
	if err != nil {
		return fmt.Errorf("reaper: timeline of #%d: %w", n, err)
	}
	at := fresh.UpdatedAt
	for _, ev := range tl {
		if ev.Event == "labeled" && ev.Label == label {
			at = ev.CreatedAt
		}
	}
	marker := fmt.Sprintf("%s%s at=%d -->", noRecordMarker, label, at.Unix())
	cs, err := r.d.Reader.ListComments(ctx, n, time.Time{})
	if err != nil {
		return fmt.Errorf("reaper: comments of #%d: %w", n, err)
	}
	posted := false
	for _, c := range cs {
		if strings.HasPrefix(c.Body, marker) {
			posted = true
			break
		}
	}
	if !posted {
		body := marker + "\n**ard · reaper · failed**\n\n`:running` with no record in local state\n\n" +
			"The label `" + label + "` is set but no job of this controller accounts for it, so it was marked failed."
		if _, err := r.d.Effects.CreateComment(ctx, n, body); err != nil {
			return fmt.Errorf("reaper: comment on #%d: %w", n, err)
		}
	}
	if err := r.d.Effects.AddLabels(ctx, n, labels.ExecutorFailed); err != nil {
		return fmt.Errorf("reaper: add %s on #%d: %w", labels.ExecutorFailed, n, err)
	}
	if err := r.d.Effects.RemoveLabel(ctx, n, label); err != nil && !errors.Is(err, github.ErrNotFound) {
		return fmt.Errorf("reaper: remove %s on #%d: %w", label, n, err)
	}
	data, _ := json.Marshal(map[string]any{"number": n, "label": label, "reason": "no_record"})
	if err := r.d.Store.AppendEvent(ctx, state.Event{Kind: state.EventFailure, Issue: n, Stage: stage, Role: role, Data: data}); err != nil {
		return fmt.Errorf("reaper: record failure of #%d: %w", n, err)
	}
	r.log().Warn("reaper.norecord", "issue", n, "stage", stage, "role", role, "label", label)
	return nil
}

// hasJob reports whether a running job, or one that ended within the grace
// period, accounts for the target n of stage and role (the PR for a review).
func (r *Reaper) hasJob(ctx context.Context, stage, role string, n int) (bool, error) {
	running, err := r.d.Store.RunningJobs(ctx)
	if err != nil {
		return false, fmt.Errorf("reaper: no-record re-check of #%d: %w", n, err)
	}
	recent, err := r.d.Store.EndedSince(ctx, r.now().Add(-noRecordGrace))
	if err != nil {
		return false, fmt.Errorf("reaper: no-record re-check of #%d: %w", n, err)
	}
	for _, sj := range append(running, recent...) {
		t := sj.Issue
		if sj.Stage == string(labels.StageReview) {
			t = sj.PR
		}
		if sj.Stage == stage && sj.Role == role && t == n {
			return true, nil
		}
	}
	return false, nil
}
