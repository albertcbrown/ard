package triage

import (
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/intake"
	"github.com/albertcbrown/ard/internal/state"
)

func TestGate(t *testing.T) {
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("b", 64)
	at := t0.Add(time.Hour)
	stored := func(hash string) *state.Admission {
		return &state.Admission{Issue: 5, Mode: "label", Reason: "authorised-labeler", LabeledBy: "maint", LabeledAt: at, BodyHash: hash, RegatedAt: at}
	}
	admit := func(r intake.Reason, when time.Time) intake.Decision {
		return intake.Decision{Outcome: intake.OutcomeAdmit, Reason: r, Actor: "maint", At: when}
	}
	noRegate := func(a *state.Admission) *state.Admission { a.RegatedAt = time.Time{}; return a }
	tmpl := state.Admission{Issue: 5, Mode: "label", BodyHash: h2, LabeledAt: t0}
	tests := []struct {
		name       string
		lead       bool
		stored     *state.Admission
		d, rules   intake.Decision
		want       verdict
		wantRec    bool
		wantBy     string
		wantReason string
	}{
		{"lead without a record passes and records", true, nil, admit(intake.ReasonAuthorised, at), intake.Decision{}, vPass, true, "maint", "authorised-labeler"},
		{"lead in all mode without a record records the issue time", true, nil,
			intake.Decision{Outcome: intake.OutcomeAdmit, Reason: intake.ReasonAllMode}, intake.Decision{}, vPass, true, "", "all-mode"},
		{"lead with an equal hash passes", true, stored(h2), admit(intake.ReasonAuthorised, at), intake.Decision{}, vPass, false, "", ""},
		{"lead refused goes to the refusal action", true, nil, intake.Decision{Outcome: intake.OutcomeRefuse, Reason: intake.ReasonUnauthorised}, intake.Decision{}, vRefuse, false, "", ""},
		{"lead skipped", true, nil, intake.Decision{Outcome: intake.OutcomeSkip, Reason: intake.ReasonLabelAbsent}, intake.Decision{}, vSkip, false, "", ""},
		{"lead, changed hash, no newer relabel: re-gate", true, stored(h1), admit(intake.ReasonAuthorised, at), admit(intake.ReasonAuthorised, at), vRegate, false, "", ""},
		{"lead, changed hash, older relabel: re-gate", true, stored(h1), admit(intake.ReasonAuthorised, at.Add(-time.Minute)), admit(intake.ReasonAuthorised, at.Add(-time.Minute)), vRegate, false, "", ""},
		{"lead, changed hash, never re-gated yet: re-gate even with a later relabel", true, noRegate(stored(h1)), admit(intake.ReasonAuthorised, at.Add(time.Hour)), admit(intake.ReasonAuthorised, at.Add(time.Hour)), vRegate, false, "", ""},
		{"all mode record, authorised relabel between creation and admission, changed hash: re-gate", true,
			&state.Admission{Issue: 5, Mode: "all", Reason: "all-mode", LabeledAt: at, BodyHash: h1},
			intake.Decision{Outcome: intake.OutcomeAdmit, Reason: intake.ReasonAllMode}, admit(intake.ReasonAuthorised, t0.Add(time.Minute)), vRegate, false, "", ""},
		{"lead, changed hash, newer authorised relabel: pass and replace", true, stored(h1), admit(intake.ReasonAuthorised, at.Add(time.Hour)), admit(intake.ReasonAuthorised, at.Add(time.Hour)), vPass, true, "maint", "authorised-labeler"},
		{"all mode, changed hash, no relabel: re-gate", true, stored(h1), intake.Decision{Outcome: intake.OutcomeAdmit, Reason: intake.ReasonAllMode},
			intake.Decision{Outcome: intake.OutcomeSkip, Reason: intake.ReasonLabelAbsent}, vRegate, false, "", ""},
		{"all mode, the App labeled after the record, body changed: re-gate", true, stored(h1), intake.Decision{Outcome: intake.OutcomeAdmit, Reason: intake.ReasonAllMode},
			intake.Decision{Outcome: intake.OutcomeAdmit, Reason: intake.ReasonAppLabeled, At: at.Add(time.Hour)}, vRegate, false, "", ""},
		{"all mode, changed hash, re-admitted through a label event", true, stored(h1), intake.Decision{Outcome: intake.OutcomeAdmit, Reason: intake.ReasonAllMode},
			admit(intake.ReasonAuthorised, at.Add(time.Hour)), vPass, true, "maint", "authorised-labeler"},
		{"other role without a record skips", false, nil, intake.Decision{}, intake.Decision{}, vSkip, false, "", ""},
		{"other role with an equal hash passes", false, stored(h2), intake.Decision{}, intake.Decision{}, vPass, false, "", ""},
		{"other role, changed hash: re-gate", false, stored(h1), intake.Decision{}, intake.Decision{Outcome: intake.OutcomeSkip}, vRegate, false, "", ""},
		{"other role, changed hash, newer authorised relabel: pass", false, stored(h1), intake.Decision{}, admit(intake.ReasonAuthorised, at.Add(time.Hour)), vPass, true, "maint", "authorised-labeler"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, rec := gate(tc.lead, tc.stored, tc.d, tc.rules, tmpl)
			if v != tc.want {
				t.Fatalf("verdict = %v, want %v", v, tc.want)
			}
			if (rec != nil) != tc.wantRec {
				t.Fatalf("record = %+v, want record %v", rec, tc.wantRec)
			}
			if rec != nil {
				if rec.BodyHash != h2 || rec.LabeledBy != tc.wantBy || rec.Reason != tc.wantReason || rec.LabeledAt.IsZero() {
					t.Errorf("record = %+v", rec)
				}
			}
		})
	}
}
