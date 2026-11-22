package triage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/intake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// metaEnabledAt holds when all mode was first seen enabled, in unix seconds,
// on GitHub's clock. "" means unset.
const metaEnabledAt = "intake.all_enabled_at"

// enabledAt returns the intake.Input.EnabledAt for this tick. In all mode it
// is taken once from GitHub's timestamps (the newest issue's created or
// updated time), never from the controller clock, which could run behind and
// admit backlog issues. Leaving all mode resets it, so re-enabling starts a
// new clock. A zero result fails closed to the label rules.
func (l *Loop) enabledAt(ctx context.Context, cfg *config.Resolved) (time.Time, error) {
	st := l.d.Store
	v, err := st.Meta(ctx, metaEnabledAt)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return time.Time{}, fmt.Errorf("triage: read %s: %w", metaEnabledAt, err)
	}
	if cfg.Intake.Mode != config.IntakeAll {
		if v != "" {
			if err := st.SetMeta(ctx, metaEnabledAt, ""); err != nil {
				return time.Time{}, fmt.Errorf("triage: clear %s: %w", metaEnabledAt, err)
			}
		}
		return time.Time{}, nil
	}
	if v != "" {
		if sec, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			return time.Unix(sec, 0).UTC(), nil
		}
	}
	res, err := l.d.Reader.SearchIssues(ctx, "is:issue", github.SearchOpts{Sort: "updated", Order: "desc", Max: 1})
	if err != nil {
		return time.Time{}, fmt.Errorf("triage: find all-mode start: %w", err)
	}
	if len(res) == 0 {
		return time.Time{}, nil
	}
	at := res[0].CreatedAt
	if res[0].UpdatedAt.After(at) {
		at = res[0].UpdatedAt
	}
	if err := st.SetMeta(ctx, metaEnabledAt, strconv.FormatInt(at.Unix(), 10)); err != nil {
		return time.Time{}, fmt.Errorf("triage: store %s: %w", metaEnabledAt, err)
	}
	return at.UTC(), nil
}

// judge gathers what intake.Admit needs for one issue, lazily: the timeline
// once, and a permission per labeler.
type judge struct {
	t      *tick
	is     github.Issue
	tl     []github.TimelineEvent
	loaded bool
	perms  map[string]github.Permission
}

func (t *tick) judge(is github.Issue) *judge {
	return &judge{t: t, is: is, perms: map[string]github.Permission{}}
}

// decide runs Admit under ic. A read error means no decision this poll: the
// caller must not act.
func (j *judge) decide(ctx context.Context, ic config.Intake) (intake.Decision, error) {
	rd := j.t.l.d.Reader
	if !j.loaded {
		tl, err := rd.ListTimeline(ctx, j.is.Number)
		if err != nil {
			return intake.Decision{}, fmt.Errorf("triage issue %d: timeline: %w", j.is.Number, err)
		}
		j.tl, j.loaded = tl, true
	}
	in := intake.Input{Issue: j.is, Timeline: j.tl, Self: j.t.self, EnabledAt: j.t.enabledAt, Perms: j.perms}
	if login, ok := intake.Labeler(ic, in); ok {
		if _, have := j.perms[login]; !have {
			p, err := rd.GetPermission(ctx, login)
			if err != nil {
				return intake.Decision{}, fmt.Errorf("triage issue %d: permission of labeler: %w", j.is.Number, err)
			}
			j.perms[login] = p
		}
	}
	return intake.Admit(ic, in), nil
}

type verdict int

const (
	vPass verdict = iota
	vSkip
	vRefuse
	vRegate
)

func (v verdict) String() string { return [...]string{"pass", "skip", "refuse", "regate"}[v] }

// gate decides a triage claim on a human-authored issue. stored is nil when no
// admission exists. d is Admit under the configured intake (lead only);
// labelRules is Admit under label mode with the configured label (read only
// when the hash changed). tmpl carries the issue, mode, current hash and the
// issue's creation time (as LabeledAt) for a new record. The returned record,
// when not nil, replaces the stored one.
//
// A body edit re-gates (and records RegatedAt) unless a maintainer re-applied
// the label after the edit was first seen. An app-labeled event never re-admits, or the sweep's own
// ard:pending would defeat the re-gate in all mode.
func gate(lead bool, stored *state.Admission, d, labelRules intake.Decision, tmpl state.Admission) (verdict, *state.Admission) {
	if lead && d.Outcome != intake.OutcomeAdmit {
		if d.Outcome == intake.OutcomeRefuse {
			return vRefuse, nil
		}
		return vSkip, nil
	}
	if stored == nil {
		if !lead {
			return vSkip, nil // a request label on a human issue that never went through the lead
		}
		r := tmpl
		r.Reason, r.LabeledBy = string(d.Reason), d.Actor
		if !d.At.IsZero() {
			r.LabeledAt = d.At
		}
		return vPass, &r
	}
	if stored.BodyHash == tmpl.BodyHash {
		return vPass, nil
	}
	if labelRules.Outcome == intake.OutcomeAdmit && labelRules.Reason == intake.ReasonAuthorised && !stored.RegatedAt.IsZero() && stored.RegatedHash == tmpl.BodyHash && labelRules.At.After(stored.RegatedAt) {
		r := tmpl
		r.Reason, r.LabeledBy, r.LabeledAt = string(labelRules.Reason), labelRules.Actor, labelRules.At
		return vPass, &r
	}
	return vRegate, nil
}

// excludedFromSweep are the labels that keep an issue out of the intake sweep.
func excludedFromSweep() []string {
	return []string{labels.Pending, labels.Running(labels.StageTriage, leadRole), labels.Done(labels.StageTriage, leadRole),
		labels.ExecutorFailed, labels.NeedsHuman, labels.Hold}
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

// sweep admits human-filed issues in all mode or with a custom intake label by
// adding ard:pending. Default label mode needs no sweep: the intake label is
// ard:pending itself and the lead's query finds it.
func (t *tick) sweep(ctx context.Context) []error {
	cfg := t.cfg
	label := intake.Label(cfg.Intake)
	custom := !strings.EqualFold(label, labels.Pending)
	all := cfg.Intake.Mode == config.IntakeAll
	if !all && !custom {
		return nil
	}
	rd := t.l.d.Reader
	var errs []error
	found := map[int]github.Issue{}
	if all && !t.enabledAt.IsZero() {
		is, err := rd.ListIssues(ctx, github.ListIssuesOpts{State: github.StateOpen, Since: t.enabledAt})
		if err != nil {
			errs = append(errs, fmt.Errorf("triage: list new issues: %w", err))
		}
		for _, i := range is {
			if i.CreatedAt.After(t.enabledAt) {
				found[i.Number] = i
			}
		}
	}
	if custom {
		is, err := rd.ListIssues(ctx, github.ListIssuesOpts{State: github.StateOpen, Labels: []string{label}})
		if err != nil {
			errs = append(errs, fmt.Errorf("triage: list issues labelled %s: %w", label, err))
		}
		for _, i := range is {
			found[i.Number] = i
		}
	}
	nums := make([]int, 0, len(found))
	for n := range found {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		is := found[n]
		if is.IsPR || strings.EqualFold(is.Author, t.self) || hasAny(is.Labels, excludedFromSweep()) {
			continue
		}
		d, err := t.judge(is).decide(ctx, cfg.Intake)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		t.log.Info("triage.intake", "issue", n, "outcome", string(d.Outcome), "reason", string(d.Reason), "actor", d.Actor)
		switch d.Outcome {
		case intake.OutcomeAdmit:
			if t.dry {
				t.log.Info("triage.would_label", "issue", n, "label", labels.Pending)
				continue
			}
			if err := t.l.d.Effects.AddLabels(ctx, n, labels.Pending); err != nil {
				errs = append(errs, fmt.Errorf("triage issue %d: add %s: %w", n, labels.Pending, err))
				continue
			}
			t.event(ctx, n, "label_pending", string(d.Outcome), string(d.Reason), d.Actor)
		case intake.OutcomeRefuse:
			if err := t.refuse(ctx, is, d); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func (t *tick) postOnce(ctx context.Context, n int, marker, text string) error {
	return agentjob.PostOnce(ctx, t.l.d.Reader, t.l.d.Effects, n, marker, text)
}

// refuse is the action for intake.OutcomeRefuse: one comment (never naming
// the labeler or quoting issue text), then ard:needs-human, then the event. The
// order makes a replay safe.
func (t *tick) refuse(ctx context.Context, is github.Issue, d intake.Decision) error {
	n := is.Number
	label := intake.Label(t.cfg.Intake)
	if t.dry {
		t.log.Info("triage.would_refuse", "issue", n, "reason", string(d.Reason))
		return nil
	}
	marker := fmt.Sprintf("<!-- %sintake refuse label=%s at=%d -->", labels.Prefix, label, d.At.Unix())
	text := fmt.Sprintf("**ard · triage · not started**\n\nThe most recent `%s` label on this issue was added by an account without triage permission, so ard will not triage it. "+
		"A maintainer can re-apply `%s` and remove `%s` to start triage.", label, label, labels.NeedsHuman)
	if err := t.postOnce(ctx, n, marker, text); err != nil {
		return err
	}
	if err := t.l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		return fmt.Errorf("triage issue %d: add %s: %w", n, labels.NeedsHuman, err)
	}
	t.event(ctx, n, "refuse", string(d.Outcome), string(d.Reason), d.Actor)
	return nil
}

// regate is the action for an issue whose title or body changed after it was
// admitted. It mirrors refuse.
func (t *tick) regate(ctx context.Context, is github.Issue, hash string) error {
	n := is.Number
	label := intake.Label(t.cfg.Intake)
	if t.dry {
		t.log.Info("triage.would_regate", "issue", n)
		return nil
	}
	if err := t.markRegated(ctx, is, hash); err != nil {
		return err
	}
	marker := fmt.Sprintf("<!-- %striage regate body=%s -->", labels.Prefix, hash[:12])
	text := fmt.Sprintf("**ard · triage · paused**\n\nThe title or body of this issue changed after it was admitted for triage, so ard stopped. "+
		"A maintainer can review the change, re-apply `%s` and remove `%s` to continue.", label, labels.NeedsHuman)
	if err := t.postOnce(ctx, n, marker, text); err != nil {
		return err
	}
	if err := t.l.d.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		return fmt.Errorf("triage issue %d: add %s: %w", n, labels.NeedsHuman, err)
	}
	t.event(ctx, n, "regate", "regate", "body-changed", "")
	return nil
}

func (t *tick) markRegated(ctx context.Context, is github.Issue, hash string) error {
	return agentjob.MarkRegated(ctx, t.l.d.Store, is, hash)
}
