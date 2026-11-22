package state

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAdmission(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	if _, err := s.Admission(ctx, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty: %v", err)
	}
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("b", 64)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a := Admission{Issue: 5, Mode: "label", Reason: "authorised-labeler", LabeledBy: "maint", LabeledAt: at, BodyHash: h1, JobID: "J1"}
	if err := s.SetAdmission(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Admission(ctx, 5)
	if err != nil || got.LabeledBy != "maint" || !got.LabeledAt.Equal(at) || got.BodyHash != h1 || got.JobID != "J1" || got.AdmittedAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
	a.BodyHash, a.LabeledBy, a.JobID = h2, "", ""
	if err := s.SetAdmission(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Admission(ctx, 5); got.BodyHash != h2 || got.LabeledBy != "" || got.JobID != "" {
		t.Fatalf("upsert: %+v", got)
	}
	for name, bad := range map[string]Admission{
		"issue":   {Mode: "all", Reason: "r", LabeledAt: at, BodyHash: h1},
		"mode":    {Issue: 1, Reason: "r", LabeledAt: at, BodyHash: h1},
		"reason":  {Issue: 1, Mode: "all", LabeledAt: at, BodyHash: h1},
		"hash":    {Issue: 1, Mode: "all", Reason: "r", LabeledAt: at, BodyHash: "xyz"},
		"labeled": {Issue: 1, Mode: "all", Reason: "r", BodyHash: h1},
	} {
		if err := s.SetAdmission(ctx, bad); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
