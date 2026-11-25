package servecmd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/controller"
)

type namedLoop struct {
	controller.Loop
	name string
}

func (l namedLoop) Name() string { return l.name }

func loopsNamed(names ...string) []controller.Loop {
	var ls []controller.Loop
	for _, n := range names {
		ls = append(ls, namedLoop{name: n})
	}
	return ls
}

// The soak runs only the deterministic loops (#187); an empty --loops keeps all.
func TestSelectLoops(t *testing.T) {
	all := loopsNamed(knownLoops...)
	tests := []struct {
		name string
		want []string
		got  []string
	}{
		{"empty keeps every loop", nil, knownLoops},
		{"soak set, in registration order", []string{"reaper", "scanners", "stale"}, []string{"scanners", "stale", "reaper"}},
		{"spaces trimmed, duplicates once", []string{" stale", "stale"}, []string{"stale"}},
		{"prwatch is selectable", []string{"prwatch", "merger"}, []string{"merger", "prwatch"}},
		{"a known loop that is not registered is not an error", []string{"triage"}, []string{"triage"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := selectLoops(all, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := loopNames(sel); !reflect.DeepEqual(got, tc.got) {
				t.Errorf("selected %v, want %v", got, tc.got)
			}
		})
	}
	sel, err := selectLoops(loopsNamed("scanners", "stale"), []string{"triage"})
	if err != nil || len(sel) != 0 {
		t.Errorf("triage not registered: got %v, %v", loopNames(sel), err)
	}
}

func TestSelectLoopsUnknownName(t *testing.T) {
	_, err := selectLoops(loopsNamed(knownLoops...), []string{"scanners", "scaners"})
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("err = %v, want ExitError code 2", err)
	}
	for _, s := range []string{`"scaners"`, "scanners, stale, triage, workers, reviewers, reviewfix, merger, prwatch, janitor, reaper, budget"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not name %s", err, s)
		}
	}
}

// An unknown loop fails before serve takes the leader lock or touches state.
func TestServeUnknownLoopFailsFirst(t *testing.T) {
	built := false
	err := serveLoops(context.Background(), &cli.Env{}, nil, []string{"nope"}, cli.K8sOpts{})
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 || built {
		t.Fatalf("err = %v, built = %v; want ExitError 2 before building", err, built)
	}
}

// The loop names registerLoops' constructors report are the known names.
func TestKnownLoopsMatchLoopNames(t *testing.T) {
	want := []string{"scanners", "stale", "triage", "workers", "reviewers", "reviewfix", "merger", "prwatch", "janitor", "reaper", "budget", "digest", "approvals"}
	if !reflect.DeepEqual(knownLoops, want) {
		t.Errorf("knownLoops = %v, want %v", knownLoops, want)
	}
}
