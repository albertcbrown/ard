package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/query"
	"github.com/albertcbrown/ard/internal/intake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// candidate is an issue a worker role may claim, with the fresh re-read.
type candidate struct {
	role  config.Role
	issue github.Issue
}

// candidates finds, per worker role, the issues that request it and pass the
// approval and admission checks. At most one candidate per issue is returned,
// and none for an issue with a running triage or worker job.
func (t *tick) candidates(ctx context.Context) ([]candidate, []error) {
	l := t.l
	running, err := l.d.Store.RunningJobs(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("worker: list running jobs: %w", err)}
	}
	busy := map[int]bool{}
	for _, j := range running {
		if j.Stage == stageWorker || j.Stage == "triage" {
			busy[j.Issue] = true
		}
	}
	var out []candidate
	var errs []error
	for _, role := range t.cfg.RolesOf(config.KindWorker) {
		q, err := query.RoleQuery{
			Type: query.TypeIssue, Request: labels.Request(labels.StageWorker, role.Name),
			States: []string{labels.Running(labels.StageWorker, role.Name), labels.Done(labels.StageWorker, role.Name)},
			Guards: []string{labels.ExecutorFailed, labels.NeedsHuman, labels.Hold},
		}.Build()
		if err != nil {
			errs = append(errs, fmt.Errorf("worker role %s: %w", role.Name, err))
			continue
		}
		hits, err := l.d.Reader.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc", Max: 50})
		if err != nil {
			errs = append(errs, fmt.Errorf("worker role %s: search: %w", role.Name, err))
			continue
		}
		for _, hit := range hits {
			if busy[hit.Number] {
				continue
			}
			c, ok, err := t.consider(ctx, role, hit.Number)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if ok {
				busy[c.issue.Number] = true
				out = append(out, c)
			}
		}
	}
	return out, errs
}

// consider re-reads issue n (search is eventually consistent), then checks
// approval and admission. #30 owns adding ard:worker:<role>; these checks are
// defence in depth.
func (t *tick) consider(ctx context.Context, role config.Role, n int) (candidate, bool, error) {
	l := t.l
	is, err := l.d.Reader.GetIssue(ctx, n)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return candidate{}, false, nil
		}
		return candidate{}, false, fmt.Errorf("worker issue %d: re-read: %w", n, err)
	}
	if is.State != github.StateOpen || is.IsPR || !hasLabel(is.Labels, labels.Request(labels.StageWorker, role.Name)) ||
		hasAny(is.Labels, excludedLabels(role.Name)) {
		return candidate{}, false, nil
	}
	if t.cfg.RequireApproval && !hasLabel(is.Labels, labels.Approved) {
		t.log.Info("worker.awaiting_approval", "issue", n, "role", role.Name)
		return candidate{}, false, nil
	}
	c := candidate{role: role, issue: is}
	if strings.EqualFold(is.Author, t.self) { // scanner issues: the App rewrites its own bodies
		return c, true, nil
	}
	hash := agentjob.BodyHash(is)
	stored, err := l.d.Store.Admission(ctx, n)
	switch {
	case errors.Is(err, state.ErrNotFound):
		t.log.Info("worker.not_admitted", "issue", n, "role", role.Name)
		return candidate{}, false, nil
	case err != nil:
		return candidate{}, false, fmt.Errorf("worker issue %d: read admission: %w", n, err)
	case stored.BodyHash == hash:
		return c, true, nil
	}
	return candidate{}, false, t.regate(ctx, is, hash)
}

// regate is the action for an issue whose title or body changed after it was
// admitted for triage: one comment, then ard:needs-human.
func (t *tick) regate(ctx context.Context, is github.Issue, hash string) error {
	n := is.Number
	if t.dry {
		t.log.Info("worker.would_regate", "issue", n)
		return nil
	}
	if err := agentjob.MarkRegated(ctx, t.l.d.Store, is, hash); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	label := intake.Label(t.cfg.Intake)
	marker := fmt.Sprintf("<!-- %sworker regate body=%s -->", labels.Prefix, hash[:12])
	text := fmt.Sprintf("**ard · worker · paused**\n\nThe title or body of this issue changed after it was admitted, so ard stopped before changing code. "+
		"A maintainer can review the change, re-apply `%s` (which runs triage again) and remove `%s` to continue.", label, labels.NeedsHuman)
	if err := agentjob.PostOnce(ctx, t.l.d.Reader, t.l.d.Effects, n, marker, text); err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	if err := t.l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		return fmt.Errorf("worker issue %d: add %s: %w", n, labels.NeedsHuman, err)
	}
	t.log.Info("worker.regate", "issue", n)
	return nil
}

func hasAny(have []string, want []string) bool {
	for _, w := range want {
		if hasLabel(have, w) {
			return true
		}
	}
	return false
}

func hasLabel(have []string, want string) bool {
	for _, h := range have {
		if strings.EqualFold(h, want) {
			return true
		}
	}
	return false
}
