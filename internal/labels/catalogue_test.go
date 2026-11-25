package labels

import (
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/albertcbrown/ard/internal/config"
)

var update = flag.Bool("update", false, "rewrite testdata/catalogue.golden")

var exampleSpec = Spec{
	Triage:    []string{"lead", "product-manager", "architect", "designer", "documentation-planner", "engineer"},
	Workers:   []string{"engineer"},
	Reviewers: []string{"security-review", "license-review", "eng-review", "product-manager-review"},
	Scanners:  []string{"cve", "lint"},
}

func TestDesiredGolden(t *testing.T) {
	got, err := Desired(exampleSpec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 51 {
		t.Errorf("len = %d, want 51", len(got))
	}
	var b strings.Builder
	for _, l := range got {
		b.WriteString(l.Name + "\t" + l.Color + "\t" + l.Description + "\n")
	}
	const path = "testdata/catalogue.golden"
	if *update {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("catalogue differs from %s (run go test ./internal/labels -update):\n%s", path, b.String())
	}
}

func TestDesiredEmptySpec(t *testing.T) {
	got, err := Desired(Spec{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 13 {
		t.Fatalf("len = %d, want the thirteen statics", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name >= got[i].Name {
			t.Errorf("not sorted at %d: %q >= %q", i, got[i-1].Name, got[i].Name)
		}
	}
}

func TestDesiredInvariants(t *testing.T) {
	got, err := Desired(exampleSpec)
	if err != nil {
		t.Fatal(err)
	}
	hex := regexp.MustCompile(`^[0-9a-f]{6}$`)
	for _, l := range got {
		if !hex.MatchString(l.Color) {
			t.Errorf("%s: colour %q", l.Name, l.Color)
		}
		if l.Description == "" || strings.Contains(l.Description, "%") {
			t.Errorf("%s: description %q", l.Name, l.Description)
		}
		if _, ok := Parse(l.Name); !ok {
			t.Errorf("Parse(%q) failed: Desired emits a name Parse rejects", l.Name)
		}
	}
	// The same checks with a custom intake label. It is the one name Parse
	// does not know, so skip the round-trip for it only.
	sp := exampleSpec
	sp.Intake = "ard:intake"
	got, err = Desired(sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		if !hex.MatchString(l.Color) {
			t.Errorf("%s: colour %q", l.Name, l.Color)
		}
		if l.Description == "" || strings.Contains(l.Description, "%") || len(l.Description) > maxDescLen {
			t.Errorf("%s: description %q", l.Name, l.Description)
		}
		if l.Name == sp.Intake {
			continue
		}
		if _, ok := Parse(l.Name); !ok {
			t.Errorf("Parse(%q) failed: Desired emits a name Parse rejects", l.Name)
		}
	}
}

func TestDesiredIntake(t *testing.T) {
	tests := []struct {
		name     string
		spec     Spec
		wantName string // "" = no extra label
		wantLen  int
		wantErr  []string
		noErr    string // must not appear in the error
	}{
		{name: "default", spec: Spec{}, wantLen: 13},
		{name: "pending", spec: Spec{Intake: "ard:pending"}, wantLen: 13},
		{name: "pending upper", spec: Spec{Intake: "ARD:PENDING"}, wantLen: 13},
		{name: "custom", spec: Spec{Intake: "ard:intake"}, wantName: "ard:intake", wantLen: 14},
		{name: "clash static", spec: Spec{Intake: "ard:needs-human"}, wantErr: []string{"intake.label", "uses itself"}, noErr: "duplicate"},
		{name: "clash stage", spec: Spec{Intake: "ard:worker:engineer", Workers: []string{"engineer"}}, wantErr: []string{"intake.label"}, noErr: "duplicate"},
		{name: "clash and bad role", spec: Spec{Intake: "ard:needs-human", Workers: []string{"Foo"}}, wantErr: []string{"intake.label", `worker "Foo"`}},
		{name: "50 runes", spec: Spec{Intake: "ard:" + strings.Repeat("é", 46)}, wantName: "ard:" + strings.Repeat("é", 46), wantLen: 14},
		{name: "51 runes", spec: Spec{Intake: "ard:" + strings.Repeat("é", 47)}, wantErr: []string{"intake.label: label"}},
		{name: "too long", spec: Spec{Intake: "ard:" + strings.Repeat("a", 47)}, wantErr: []string{"intake.label: label"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Desired(tt.spec)
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("want error containing %q, got none", tt.wantErr)
				}
				for _, w := range tt.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("err = %v, want containing %q", err, w)
					}
				}
				if tt.noErr != "" && strings.Contains(err.Error(), tt.noErr) {
					t.Errorf("err = %v, must not contain %q", err, tt.noErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i := 1; i < len(got); i++ {
				if got[i-1].Name >= got[i].Name {
					t.Errorf("not sorted at %d", i)
				}
			}
			if tt.wantName == "" {
				return
			}
			found := false
			for _, l := range got {
				if l.Name == tt.wantName {
					found = true
					if l.Color != "ededed" || l.Description != intakeStyle.desc {
						t.Errorf("intake label = %+v", l)
					}
				}
			}
			if !found {
				t.Errorf("%s missing from the catalogue", tt.wantName)
			}
		})
	}
}

func TestSpecFromIntake(t *testing.T) {
	tests := []struct{ name, cfg, want string }{
		{"label mode with label", "intake: {mode: label, label: \"ard:intake\"}\n", "ard:intake"},
		{"label mode default", "intake: {mode: label}\n", ""},
		{"all mode", "intake: {mode: all}\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := config.LoadFS(fstest.MapFS{".ard/config.yaml": {Data: []byte(tt.cfg)}}, "/repo")
			if err != nil {
				t.Fatal(err)
			}
			if got := SpecFrom(res).Intake; got != tt.want {
				t.Errorf("Intake = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDesiredLimits(t *testing.T) {
	r := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name    string
		spec    Spec
		wantErr string // "" = ok
	}{
		{"reviewer 31", Spec{Reviewers: []string{r(31)}}, ""},
		{"reviewer 32", Spec{Reviewers: []string{r(32)}}, `reviewer "` + r(32) + `"`},
		{"triage 31", Spec{Triage: []string{r(31)}}, ""},
		{"triage 32", Spec{Triage: []string{r(32)}}, "triage role"},
		{"worker 32", Spec{Workers: []string{r(32)}}, "worker"},
		{"scanner 38", Spec{Scanners: []string{r(38)}}, ""},
		{"scanner 39", Spec{Scanners: []string{r(39)}}, "scanner"},
		{"uppercase", Spec{Workers: []string{"Foo"}}, `worker "Foo": invalid name`},
		{"leading dash", Spec{Scanners: []string{"-x"}}, `scanner "-x": invalid name`},
		{"colon", Spec{Reviewers: []string{"a:b"}}, `reviewer "a:b": invalid name`},
	}
	for _, tt := range tests {
		_, err := Desired(tt.spec)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%s: err = %v, want containing %q", tt.name, err, tt.wantErr)
		}
	}
	// Every problem is reported, not just the first.
	_, err := Desired(Spec{Workers: []string{"Foo"}, Scanners: []string{"-x"}})
	if err == nil || !strings.Contains(err.Error(), "Foo") || !strings.Contains(err.Error(), "-x") {
		t.Errorf("want both errors, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	long := strings.Repeat("d", 101)
	tests := []struct {
		name    string
		in      []Label
		wantErr string
	}{
		{"ok", []Label{{Name: "ard:hold", Color: "ffffff", Description: "x"}}, ""},
		{"name 51", []Label{{Name: "ard:review:" + strings.Repeat("a", 32) + ":changes"}}, "is 51 chars (max 50)"},
		{"description 101", []Label{{Name: "ard:hold", Description: long}}, "description is 101 chars"},
		{"description 100", []Label{{Name: "ard:hold", Description: long[:100]}}, ""},
		{"duplicate case-insensitive", []Label{{Name: "ard:hold"}, {Name: "ARD:Hold"}}, "duplicate"},
	}
	for _, tt := range tests {
		err := validate(tt.in, "")
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%s: err = %v, want containing %q", tt.name, err, tt.wantErr)
		}
	}
}

func TestSpecFrom(t *testing.T) {
	fsys := fstest.MapFS{
		".ard/scanners/cve/scan":           {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
		".ard/triage/lead/prompt.txt":      {Data: []byte("x")},
		".ard/triage/architect/prompt.txt": {Data: []byte("x")},
		".ard/workers/engineer/prompt.txt": {Data: []byte("x")},
		".ard/reviewers/sec/prompt.txt":    {Data: []byte("x")},
	}
	res, err := config.LoadFS(fsys, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	got := SpecFrom(res)
	join := func(s []string) string { return strings.Join(s, ",") }
	if join(got.Triage) != "architect,lead" || join(got.Workers) != "engineer" ||
		join(got.Reviewers) != "sec" || join(got.Scanners) != "cve" {
		t.Errorf("SpecFrom = %+v", got)
	}
}

func TestStageOf(t *testing.T) {
	tests := []struct {
		k    config.Kind
		want Stage
		ok   bool
	}{
		{config.KindTriage, StageTriage, true},
		{config.KindWorker, StageWorker, true},
		{config.KindReviewer, StageReview, true},
		{config.KindScanner, "", false},
	}
	for _, tt := range tests {
		if got, ok := StageOf(tt.k); got != tt.want || ok != tt.ok {
			t.Errorf("StageOf(%q) = %q, %v", tt.k, got, ok)
		}
	}
}
