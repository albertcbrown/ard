package intake

import (
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const self = "ard[bot]"

func ev(kind, actor, label string, min int) github.TimelineEvent {
	return github.TimelineEvent{Event: kind, Actor: actor, Label: label, CreatedAt: t0.Add(time.Duration(min) * time.Minute)}
}

func perms(m map[string]string) map[string]github.Permission {
	out := map[string]github.Permission{}
	for k, v := range m {
		out[k] = github.Permission{Role: v}
	}
	return out
}

var labelMode = config.Intake{Mode: config.IntakeLabel}

func TestLabel(t *testing.T) {
	if got := Label(config.Intake{Mode: config.IntakeLabel}); got != labels.Pending {
		t.Errorf("Label default = %q", got)
	}
	if got := Label(config.Intake{Mode: config.IntakeLabel, Label: "ard:intake"}); got != "ard:intake" {
		t.Errorf("Label custom = %q", got)
	}
}

func TestAdmit(t *testing.T) {
	pending := labels.Pending
	human := github.Issue{Author: "alice", Labels: []string{pending}, CreatedAt: t0}
	withLabels := func(l ...string) github.Issue { i := human; i.Labels = l; return i }
	lab := func(actor string) []github.TimelineEvent {
		return []github.TimelineEvent{ev("labeled", actor, pending, 1)}
	}

	tests := []struct {
		name       string
		cfg        config.Intake
		in         Input
		want       Outcome
		reason     Reason
		actor      string
		wantLookup string // Labeler login, "" = none
	}{
		{"all mode admits issue opened after enablement", config.Intake{Mode: config.IntakeAll},
			Input{Issue: github.Issue{Author: "alice", CreatedAt: t0.Add(time.Second)}, Self: self, EnabledAt: t0},
			OutcomeAdmit, ReasonAllMode, "", ""},
		{"all mode boundary: created exactly at enablement is old", config.Intake{Mode: config.IntakeAll},
			Input{Issue: github.Issue{Author: "alice", CreatedAt: t0}, Self: self, EnabledAt: t0},
			OutcomeSkip, ReasonLabelAbsent, "", ""},
		{"all mode: older issue needs the label", config.Intake{Mode: config.IntakeAll},
			Input{Issue: github.Issue{Author: "alice", CreatedAt: t0.Add(-time.Hour)}, Self: self, EnabledAt: t0},
			OutcomeSkip, ReasonLabelAbsent, "", ""},
		{"all mode: older issue with an authorised label is admitted", config.Intake{Mode: config.IntakeAll},
			Input{Issue: github.Issue{Author: "alice", Labels: []string{pending}, CreatedAt: t0.Add(-time.Hour)}, Timeline: lab("bob"), Self: self, EnabledAt: t0, Perms: perms(map[string]string{"bob": "write"})},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"all mode: unknown EnabledAt fails closed", config.Intake{Mode: config.IntakeAll},
			Input{Issue: github.Issue{Author: "alice", CreatedAt: t0}, Self: self},
			OutcomeSkip, ReasonLabelAbsent, "", ""},
		{"all mode skips a PR", config.Intake{Mode: config.IntakeAll},
			Input{Issue: github.Issue{Author: "alice", IsPR: true, CreatedAt: t0.Add(time.Hour)}, EnabledAt: t0},
			OutcomeSkip, ReasonPR, "", ""},
		{"app-authored without label", labelMode,
			Input{Issue: github.Issue{Author: self}, Self: self},
			OutcomeAdmit, ReasonAppAuthored, "", ""},
		{"label absent", labelMode,
			Input{Issue: withLabels("bug"), Self: self},
			OutcomeSkip, ReasonLabelAbsent, "", ""},
		{"triage admitted", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self, Perms: perms(map[string]string{"bob": "triage"})},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"write admitted", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self, Perms: perms(map[string]string{"bob": "write"})},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"maintain admitted", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self, Perms: perms(map[string]string{"bob": "maintain"})},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"admin admitted", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self, Perms: perms(map[string]string{"bob": "admin"})},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"read refused", labelMode, Input{Issue: human, Timeline: lab("eve"), Self: self, Perms: perms(map[string]string{"eve": "read"})},
			OutcomeRefuse, ReasonUnauthorised, "eve", "eve"},
		{"level none refused", labelMode, Input{Issue: human, Timeline: lab("eve"), Self: self, Perms: map[string]github.Permission{"eve": {Level: "none"}}},
			OutcomeRefuse, ReasonUnauthorised, "eve", "eve"},
		{"legacy role empty, level write admitted", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self, Perms: map[string]github.Permission{"bob": {Level: "write"}}},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"legacy role empty, level read refused", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self, Perms: map[string]github.Permission{"bob": {Level: "read"}}},
			OutcomeRefuse, ReasonUnauthorised, "bob", "bob"},
		{"labelled by the app", labelMode, Input{Issue: human, Timeline: lab(self), Self: self},
			OutcomeAdmit, ReasonAppLabeled, self, ""},
		{"authorised then unlabeled then read user relabels: refused",
			labelMode, Input{Issue: human, Self: self, Perms: perms(map[string]string{"bob": "write", "eve": "read"}), Timeline: []github.TimelineEvent{
				ev("labeled", "bob", pending, 1), ev("unlabeled", "bob", pending, 2), ev("labeled", "eve", pending, 3)}},
			OutcomeRefuse, ReasonUnauthorised, "eve", "eve"},
		{"read user then authorised relabel: admitted",
			labelMode, Input{Issue: human, Self: self, Perms: perms(map[string]string{"bob": "write", "eve": "read"}), Timeline: []github.TimelineEvent{
				ev("labeled", "eve", pending, 1), ev("unlabeled", "eve", pending, 2), ev("labeled", "bob", pending, 3)}},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"label present, no event", labelMode, Input{Issue: human, Self: self},
			OutcomeSkip, ReasonNoLabelEvent, "", ""},
		{"label present, last event unlabeled", labelMode, Input{Issue: human, Self: self, Timeline: []github.TimelineEvent{
			ev("labeled", "bob", pending, 1), ev("unlabeled", "bob", pending, 2)}},
			OutcomeSkip, ReasonNoLabelEvent, "", ""},
		{"empty actor refused", labelMode, Input{Issue: human, Timeline: lab(""), Self: self},
			OutcomeRefuse, ReasonUnknownActor, "", ""},
		{"missing perms entry skips", labelMode, Input{Issue: human, Timeline: lab("bob"), Self: self},
			OutcomeSkip, ReasonPermUnknown, "bob", "bob"},
		{"events for other labels ignored", labelMode, Input{Issue: human, Self: self, Perms: perms(map[string]string{"bob": "write", "eve": "read"}), Timeline: []github.TimelineEvent{
			ev("labeled", "bob", pending, 1), ev("labeled", "eve", labels.Hold, 2), ev("unlabeled", "eve", "bug", 3)}},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"custom label, authorised labeler", config.Intake{Mode: config.IntakeLabel, Label: "ard:intake"},
			Input{Issue: withLabels("ard:intake"), Self: self, Perms: perms(map[string]string{"bob": "write"}),
				Timeline: []github.TimelineEvent{ev("labeled", "bob", "ard:intake", 1)}},
			OutcomeAdmit, ReasonAuthorised, "bob", "bob"},
		{"custom label: pending applied by the app does not count", config.Intake{Mode: config.IntakeLabel, Label: "ard:intake"},
			Input{Issue: human, Timeline: lab(self), Self: self},
			OutcomeSkip, ReasonLabelAbsent, "", ""},
		{"case folding", labelMode,
			Input{Issue: withLabels("ard:Pending"), Self: "ARD[bot]", Timeline: []github.TimelineEvent{ev("labeled", "ard[bot]", "ARD:PENDING", 1)}},
			OutcomeAdmit, ReasonAppLabeled, "ard[bot]", ""},
		{"empty Self and Author is not app-authored", labelMode,
			Input{Issue: github.Issue{Author: ""}},
			OutcomeSkip, ReasonLabelAbsent, "", ""},
		{"zero config is invalid", config.Intake{}, Input{Issue: human, Self: self},
			OutcomeSkip, ReasonBadConfig, "", ""},
		{"PR skipped in label mode", labelMode, Input{Issue: github.Issue{IsPR: true, Author: self}, Self: self},
			OutcomeSkip, ReasonPR, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Admit(tt.cfg, tt.in)
			if got.Outcome != tt.want || got.Reason != tt.reason || got.Actor != tt.actor {
				t.Errorf("Admit = %+v, want %s/%s actor %q", got, tt.want, tt.reason, tt.actor)
			}
			if tt.actor != "" && got.At.IsZero() {
				t.Errorf("At not set: %+v", got)
			}
			login, ok := Labeler(tt.cfg, tt.in)
			if ok != (tt.wantLookup != "") || login != tt.wantLookup {
				t.Errorf("Labeler = %q, %v, want %q", login, ok, tt.wantLookup)
			}
		})
	}
}
