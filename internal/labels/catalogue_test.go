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
	if len(got) != 47 {
		t.Errorf("len = %d, want 47", len(got))
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
	if len(got) != 9 {
		t.Fatalf("len = %d, want the nine statics", len(got))
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
		err := validate(tt.in)
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
