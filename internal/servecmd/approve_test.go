package servecmd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
)

type approveFx struct {
	effects.Effects
	err   error
	dry   bool
	calls [][2]any
}

func (f *approveFx) ApproveIssue(_ context.Context, repo string, n int) error {
	f.calls = append(f.calls, [2]any{repo, n})
	return f.err
}
func (f *approveFx) DryRun() bool { return f.dry }

func TestApproveFnSuccessWakesTriage(t *testing.T) {
	fx := &approveFx{}
	var wakes [][]string
	wake := func(reason string, names ...string) { wakes = append(wakes, append([]string{reason}, names...)) }
	line, err := approveFn(fx, wake, "o/r", func(string) error { t.Fatal("browser opened"); return nil })(42)
	if err != nil || !strings.Contains(line, "approved #42") {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if !reflect.DeepEqual(fx.calls, [][2]any{{"o/r", 42}}) || !reflect.DeepEqual(wakes, [][]string{{"ui-approve", "triage"}}) {
		t.Fatalf("calls=%v wakes=%v", fx.calls, wakes)
	}
}

func TestApproveFnDryRun(t *testing.T) {
	fx := &approveFx{dry: true}
	line, err := approveFn(fx, func(string, ...string) {}, "o/r", nil)(1)
	if err != nil || !strings.Contains(line, "dry-run") {
		t.Fatalf("line=%q err=%v", line, err)
	}
}

func TestApproveFnNoGHOpensURL(t *testing.T) {
	fx := &approveFx{err: effects.ErrGHUnavailable}
	var opened string
	wake := func(string, ...string) { t.Fatal("woke triage without approving") }
	line, err := approveFn(fx, wake, "o/r", func(u string) error { opened = u; return nil })(42)
	want := "https://github.com/o/r/issues/42"
	if err != nil || opened != want || !strings.Contains(line, want) {
		t.Fatalf("line=%q opened=%q err=%v", line, opened, err)
	}
}

func TestApproveFnError(t *testing.T) {
	fx := &approveFx{err: errors.New("boom")}
	if _, err := approveFn(fx, func(string, ...string) { t.Fatal("woke") }, "o/r", nil)(1); err == nil {
		t.Fatal("error swallowed")
	}
}
