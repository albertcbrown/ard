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
		if j.Stage == stageWorker || j.Stage == "triage" || j.Stage == "review" {
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
	ok, err := t.recheck().Admitted(ctx, is)
	if err != nil || !ok {
		return candidate{}, false, err
	}
	return candidate{role: role, issue: is}, true, nil
}

// recheck is the admission re-check, shared with the reviewer (package agentjob).
func (t *tick) recheck() agentjob.Recheck {
	d := t.l.d
	return agentjob.Recheck{Stage: "worker", Stopped: "changing code", Label: intake.Label(t.cfg.Intake),
		Self: t.self, Dry: t.dry, Log: t.log, Store: d.Store, Reader: d.Reader, Effects: d.Effects}
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
