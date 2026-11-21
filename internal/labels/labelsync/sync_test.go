package labelsync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func catalogue(t *testing.T) []labels.Label {
	t.Helper()
	want, err := labels.Desired(labels.Spec{
		Triage:    []string{"lead", "product-manager", "architect", "designer", "documentation-planner", "engineer"},
		Workers:   []string{"engineer"},
		Reviewers: []string{"security-review", "license-review", "eng-review", "product-manager-review"},
		Scanners:  []string{"cve", "lint"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 47 {
		t.Fatalf("catalogue has %d labels, want 47", len(want))
	}
	return want
}

func TestDiff(t *testing.T) {
	want := []labels.Label{
		{Name: "ard:hold", Color: "f9a03f", Description: "d"},
		{Name: "ard:keep", Color: "cfd3d7", Description: "k"},
	}
	tests := []struct {
		name                           string
		have                           []github.Label
		create, update, unchanged, old int
		stale                          []string
	}{
		{"empty repo", nil, 2, 0, 0, 0, nil},
		{"all equal", []github.Label{{Name: "ard:hold", Color: "f9a03f", Description: "d"}, {Name: "ard:keep", Color: "cfd3d7", Description: "k"}}, 0, 0, 2, 0, nil},
		{"colour case and #", []github.Label{{Name: "ard:hold", Color: "#F9A03F", Description: "d"}, {Name: "ard:keep", Color: "CFD3D7", Description: "k"}}, 0, 0, 2, 0, nil},
		{"description differs", []github.Label{{Name: "ard:hold", Color: "f9a03f", Description: "x"}, {Name: "ard:keep", Color: "cfd3d7", Description: "k"}}, 0, 1, 1, 0, nil},
		{"colour differs", []github.Label{{Name: "ard:hold", Color: "000000", Description: "d"}, {Name: "ard:keep", Color: "cfd3d7", Description: "k"}}, 0, 1, 1, 0, nil},
		{"name differs in case", []github.Label{{Name: "ARD:Hold", Color: "f9a03f", Description: "d"}, {Name: "ard:keep", Color: "cfd3d7", Description: "k"}}, 0, 0, 2, 0, nil},
		{"stale and foreign", []github.Label{{Name: "ard:old"}, {Name: "bug"}, {Name: "ard:keep", Color: "cfd3d7", Description: "k"}}, 1, 0, 1, 0, []string{"ard:old"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Diff(tt.have, want)
			if len(p.Create) != tt.create || len(p.Update) != tt.update || len(p.Unchanged) != tt.unchanged {
				t.Errorf("create/update/unchanged = %d/%d/%d, want %d/%d/%d", len(p.Create), len(p.Update), len(p.Unchanged), tt.create, tt.update, tt.unchanged)
			}
			if !reflect.DeepEqual(p.Stale, tt.stale) {
				t.Errorf("stale = %v, want %v", p.Stale, tt.stale)
			}
		})
	}
}

func TestSyncLive(t *testing.T) {
	ctx := context.Background()
	want := catalogue(t)

	t.Run("second run changes nothing", func(t *testing.T) {
		f := fake.New()
		w := effects.NewLive(f, nil, quiet)
		rep, err := Sync(ctx, f, w, want, quiet)
		if err != nil || len(rep.Create) != 47 || len(f.Writes()) != 47 {
			t.Fatalf("first run: err=%v creates=%d writes=%d", err, len(rep.Create), len(f.Writes()))
		}
		before := len(f.Writes())
		rep, err = Sync(ctx, f, w, want, quiet)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Writes()) != before || len(rep.Unchanged) != 47 || len(rep.Create)+len(rep.Update) != 0 {
			t.Errorf("second run wrote %d, unchanged %d, plan %+v", len(f.Writes())-before, len(rep.Unchanged), rep.Plan)
		}
	})

	t.Run("hand-edited colour is one update", func(t *testing.T) {
		f := fake.New()
		w := effects.NewLive(f, nil, quiet)
		if _, err := Sync(ctx, f, w, want, quiet); err != nil {
			t.Fatal(err)
		}
		if err := f.UpdateLabel(ctx, labels.Hold, github.Label{Color: "000000"}); err != nil {
			t.Fatal(err)
		}
		before := len(f.Writes())
		rep, err := Sync(ctx, f, w, want, quiet)
		if err != nil || len(rep.Update) != 1 || len(f.Writes())-before != 1 {
			t.Fatalf("err=%v updates=%d writes=%d", err, len(rep.Update), len(f.Writes())-before)
		}
		got, _ := f.ListLabels(ctx)
		for _, l := range got {
			if l.Name == labels.Hold && l.Color != "f9a03f" {
				t.Errorf("colour = %q, want f9a03f", l.Color)
			}
		}
	})

	t.Run("stale label is reported and kept", func(t *testing.T) {
		f := fake.New()
		if err := f.CreateLabel(ctx, github.Label{Name: "ard:old", Color: "ffffff"}); err != nil {
			t.Fatal(err)
		}
		rep, err := Sync(ctx, f, effects.NewLive(f, nil, quiet), want, quiet)
		if err != nil || !reflect.DeepEqual(rep.Stale, []string{"ard:old"}) {
			t.Fatalf("err=%v stale=%v", err, rep.Stale)
		}
		got, _ := f.ListLabels(ctx)
		kept := false
		for _, l := range got {
			kept = kept || l.Name == "ard:old"
		}
		if !kept {
			t.Error("stale label was deleted")
		}
		for _, c := range f.Writes() {
			if strings.Contains(c.Method, "Delete") {
				t.Errorf("unexpected write %s", c.Method)
			}
		}
	})

	t.Run("one failure does not stop the rest", func(t *testing.T) {
		f := fake.New()
		boom := errors.New("boom")
		f.FailNext("CreateLabel", boom)
		rep, err := Sync(ctx, f, effects.NewLive(f, nil, quiet), want, quiet)
		if !errors.Is(err, boom) || len(rep.Failed) != 1 {
			t.Fatalf("err=%v failed=%v", err, rep.Failed)
		}
		for name, ferr := range rep.Failed {
			if !errors.Is(ferr, boom) || strings.HasPrefix(ferr.Error(), "labels sync:") {
				t.Errorf("Failed[%s] = %v, want the raw error wrapping boom", name, ferr)
			}
		}
		got, _ := f.ListLabels(ctx)
		if len(got) != 46 {
			t.Errorf("repo has %d labels, want 46", len(got))
		}
	})

	t.Run("list error writes nothing", func(t *testing.T) {
		f := fake.New()
		boom := errors.New("boom")
		f.FailNext("ListLabels", boom)
		_, err := Sync(ctx, f, effects.NewLive(f, nil, quiet), want, quiet)
		if !errors.Is(err, boom) || len(f.Writes()) != 0 {
			t.Errorf("err=%v writes=%d", err, len(f.Writes()))
		}
	})
}

func TestSyncDryRun(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	d, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Sync(ctx, f, d, catalogue(t), quiet)
	if err != nil || len(rep.Create) != 47 {
		t.Fatalf("err=%v creates=%d", err, len(rep.Create))
	}
	if n := len(f.Writes()); n != 0 {
		t.Errorf("dry-run made %d writes", n)
	}
}
