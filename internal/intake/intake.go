// Package intake decides which human-filed issues may enter triage (the
// intake setting, decided in #2). It is pure: no I/O, no context, no clock.
// The caller (the triage loop, #29) fetches the issue, its timeline and the
// labeler's permission, and Admit judges them. Admit never calls GitHub and
// fails closed.
package intake

import (
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

// Label is the label that admits an issue in label mode: cfg.Label, or
// labels.Pending when unset.
func Label(cfg config.Intake) string {
	if cfg.Label == "" {
		return labels.Pending
	}
	return cfg.Label
}

// Input is everything Admit reads. The caller fetches it; Admit never calls GitHub.
type Input struct {
	Issue    github.Issue           // reads Author, Labels, IsPR, CreatedAt only (never Body)
	Timeline []github.TimelineEvent // ListTimeline order: oldest first
	Self     string                 // the controller's login, Identity.Login ("<slug>[bot]")
	// EnabledAt is when all mode was first seen enabled. In all mode only
	// issues created after it are admitted without the label; earlier issues
	// still need the label. Zero means unknown, which fails closed to the
	// label rules. It must be on GitHub's clock (for example the API
	// response Date header, or the newest issue CreatedAt seen), never
	// time.Now(): a controller clock running behind would admit backlog issues.
	EnabledAt time.Time
	// Perms holds GetPermission results keyed by the login Labeler returned.
	Perms map[string]github.Permission
}

// Outcome is the verdict of Admit.
type Outcome string

const (
	OutcomeSkip   Outcome = "skip" // not admitted; take no action this poll (the zero value is never "admit")
	OutcomeAdmit  Outcome = "admit"
	OutcomeRefuse Outcome = "refuse" // label present but not honoured; #29 adds ard:needs-human with a comment asking a maintainer to re-apply it
)

// Reason is stable, for slog and the refusal comment.
type Reason string

const (
	ReasonAllMode      Reason = "all-mode"
	ReasonAppAuthored  Reason = "app-authored" // scanner-filed: unaffected by intake
	ReasonAppLabeled   Reason = "app-labeled"
	ReasonAuthorised   Reason = "authorised-labeler"
	ReasonPR           Reason = "pull-request"
	ReasonLabelAbsent  Reason = "label-absent"
	ReasonNoLabelEvent Reason = "no-labeled-event" // label present, timeline doesn't show it (yet)
	ReasonPermUnknown  Reason = "permission-unknown"
	ReasonUnknownActor Reason = "unknown-actor"
	ReasonUnauthorised Reason = "unauthorised-labeler" // refuse: the labeler lacks triage permission
	ReasonBadConfig    Reason = "invalid-config"
)

// Decision says whether a human-filed issue may enter triage. Actor and At
// are the labeled event Admit judged (label rules only) so #29 can record
// labeled_by.
type Decision struct {
	Outcome Outcome
	Reason  Reason
	Actor   string
	At      time.Time
}

// step is the shared walk behind Admit and Labeler: a final decision, or a
// labeler whose permission is still needed.
type step struct {
	done  bool
	d     Decision
	actor string // set when !done
	at    time.Time
}

func eval(cfg config.Intake, in Input) step {
	fin := func(o Outcome, r Reason) step { return step{done: true, d: Decision{Outcome: o, Reason: r}} }
	if in.Issue.IsPR {
		return fin(OutcomeSkip, ReasonPR)
	}
	if in.Self != "" && strings.EqualFold(in.Issue.Author, in.Self) {
		return fin(OutcomeAdmit, ReasonAppAuthored)
	}
	switch cfg.Mode {
	case config.IntakeAll:
		if !in.EnabledAt.IsZero() && in.Issue.CreatedAt.After(in.EnabledAt) {
			return fin(OutcomeAdmit, ReasonAllMode)
		}
		// Opened before all mode was enabled (or enablement unknown): the
		// label rules apply.
	case config.IntakeLabel:
	default:
		return fin(OutcomeSkip, ReasonBadConfig)
	}

	label := Label(cfg)
	// A label ard itself applies (ard:needs-human, a stage label, ...) can't
	// be the intake label: the App's own writes would admit untrusted issues.
	if labels.CheckIntake(label) != nil {
		return fin(OutcomeSkip, ReasonBadConfig)
	}
	if !hasLabel(in.Issue.Labels, label) {
		return fin(OutcomeSkip, ReasonLabelAbsent)
	}
	var last *github.TimelineEvent
	for i := range in.Timeline {
		ev := &in.Timeline[i]
		if !strings.EqualFold(ev.Label, label) {
			continue
		}
		switch ev.Event {
		case "labeled":
			last = ev
		case "unlabeled":
			last = nil // a stale snapshot: the label is gone again
		}
	}
	if last == nil {
		return fin(OutcomeSkip, ReasonNoLabelEvent)
	}
	judged := func(o Outcome, r Reason) step {
		return step{done: true, d: Decision{Outcome: o, Reason: r, Actor: last.Actor, At: last.CreatedAt}}
	}
	if last.Actor == "" {
		return judged(OutcomeRefuse, ReasonUnknownActor)
	}
	if in.Self != "" && strings.EqualFold(last.Actor, in.Self) {
		return judged(OutcomeAdmit, ReasonAppLabeled)
	}
	return step{actor: last.Actor, at: last.CreatedAt}
}

// Labeler returns the actor of the issue's current intake-label "labeled"
// event when Admit needs that actor's permission; ok is false when no lookup
// is needed (PR, App-authored, all mode admitting, label absent, no event,
// empty actor, labelled by Self).
func Labeler(cfg config.Intake, in Input) (login string, ok bool) {
	s := eval(cfg, in)
	if s.done {
		return "", false
	}
	return s.actor, true
}

// Admit decides whether a human-filed issue may enter triage. It never
// returns an error and never admits on missing data.
func Admit(cfg config.Intake, in Input) Decision {
	s := eval(cfg, in)
	if s.done {
		return s.d
	}
	d := Decision{Actor: s.actor, At: s.at}
	perm, ok := in.Perms[s.actor]
	switch {
	case !ok || (perm.Role == "" && perm.Level == ""): // missing or zero data: no side effect
		d.Outcome, d.Reason = OutcomeSkip, ReasonPermUnknown
	case perm.AtLeastTriage():
		d.Outcome, d.Reason = OutcomeAdmit, ReasonAuthorised
	default:
		d.Outcome, d.Reason = OutcomeRefuse, ReasonUnauthorised
	}
	return d
}

func hasLabel(have []string, want string) bool {
	for _, l := range have {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}
