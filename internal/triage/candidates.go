package triage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/query"
	"github.com/albertcbrown/ard/internal/intake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// candidate is an issue a role may claim, with the fresh re-read of the issue
// and the admission record the claim must write (nil when none changes).
type candidate struct {
	role   config.Role
	issue  github.Issue
	rec    *state.Admission
	reason intake.Reason
}

// candidates finds, per triage role (the lead first), the issues that request
// it and pass the gate. At most one candidate per issue is returned, and none
// for an issue that already has a running triage job.
func (t *tick) candidates(ctx context.Context) ([]candidate, []error) {
	l := t.l
	running, err := l.d.Store.RunningJobs(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("triage: list running jobs: %w", err)}
	}
	busy := map[int]bool{}
	for _, j := range running {
		if j.Stage == stageTriage {
			busy[j.Issue] = true
		}
	}
	roles := t.cfg.RolesOf(config.KindTriage)
	sort.SliceStable(roles, func(i, j int) bool {
		return roles[i].Name == leadRole && roles[j].Name != leadRole
	})
	var out []candidate
	var errs []error
	for _, role := range roles {
		q, err := query.RoleQuery{
			Type: query.TypeIssue, Request: labels.Request(labels.StageTriage, role.Name),
			States: []string{labels.Running(labels.StageTriage, role.Name), labels.Done(labels.StageTriage, role.Name)},
			Guards: []string{labels.ExecutorFailed, labels.NeedsHuman, labels.Hold},
		}.Build()
		if err != nil {
			errs = append(errs, fmt.Errorf("triage role %s: %w", role.Name, err))
			continue
		}
		hits, err := l.d.Reader.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc", Max: 50})
		if err != nil {
			errs = append(errs, fmt.Errorf("triage role %s: search: %w", role.Name, err))
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

// consider re-reads issue n (search is eventually consistent), then gates it.
func (t *tick) consider(ctx context.Context, role config.Role, n int) (candidate, bool, error) {
	l := t.l
	is, err := l.d.Reader.GetIssue(ctx, n)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return candidate{}, false, nil
		}
		return candidate{}, false, fmt.Errorf("triage issue %d: re-read: %w", n, err)
	}
	if is.State != github.StateOpen || is.IsPR || !hasLabel(is.Labels, labels.Request(labels.StageTriage, role.Name)) ||
		hasAny(is.Labels, excluded(role.Name)) {
		return candidate{}, false, nil
	}
	c := candidate{role: role, issue: is, reason: intake.ReasonAppAuthored}
	if strings.EqualFold(is.Author, t.self) { // scanner issues: the App rewrites its own bodies
		return c, true, nil
	}
	hash := BodyHash(is)
	var stored *state.Admission
	if a, err := l.d.Store.Admission(ctx, n); err == nil {
		stored = &a
	} else if !errors.Is(err, state.ErrNotFound) {
		return candidate{}, false, fmt.Errorf("triage issue %d: read admission: %w", n, err)
	}
	lead := role.Name == leadRole
	if !lead && (stored == nil || stored.BodyHash == hash) {
		if stored == nil {
			t.log.Info("triage.not_admitted", "issue", n, "role", role.Name)
			return candidate{}, false, nil
		}
		return c, true, nil
	}
	j := t.judge(is)
	var d, lr intake.Decision
	if lead {
		if d, err = j.decide(ctx, t.cfg.Intake); err != nil {
			return candidate{}, false, err
		}
	}
	if stored != nil && stored.BodyHash != hash {
		if t.cfg.Intake.Mode == config.IntakeLabel && lead {
			lr = d
		} else if lr, err = j.decide(ctx, config.Intake{Mode: config.IntakeLabel, Label: t.cfg.Intake.Label}); err != nil {
			return candidate{}, false, err
		}
	}
	tmpl := state.Admission{Issue: n, Mode: string(t.cfg.Intake.Mode), BodyHash: hash, LabeledAt: later(is.CreatedAt, is.UpdatedAt)}
	v, rec := gate(lead, stored, d, lr, tmpl)
	t.log.Info("triage.gate", "issue", n, "role", role.Name, "verdict", v.String(), "outcome", string(d.Outcome), "reason", string(d.Reason), "actor", d.Actor)
	switch v {
	case vRefuse:
		return candidate{}, false, t.refuse(ctx, is, d)
	case vRegate:
		return candidate{}, false, t.regate(ctx, is, hash)
	case vSkip:
		return candidate{}, false, nil
	}
	c.rec = rec
	if rec != nil {
		c.reason = intake.Reason(rec.Reason)
	}
	return c, true, nil
}

// later returns the later of two times. In all mode an admission has no
// labeled event, so its LabeledAt is the issue's updated_at: any label event
// before the admission is at or before it.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
