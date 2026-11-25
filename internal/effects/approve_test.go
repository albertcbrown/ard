package effects

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
)

// fakeGH puts a gh script on PATH that records argv to a file and exits with
// code after printing stderr. It returns the argv file's path.
func fakeGH(t *testing.T, code int, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + argv + "'\nprintf '%s' '" + stderr + "' >&2\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return argv
}

func newLiveT(t *testing.T) *Live {
	return NewLive(failClient{Reader: fake.New(), t: t}, gitx.New(""), slog.New(slog.DiscardHandler))
}

func TestApproveIssueRunsGH(t *testing.T) {
	argv := fakeGH(t, 0, "")
	if err := newLiveT(t).ApproveIssue(context.Background(), "o/r", 42); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argv)
	if got, want := strings.TrimSpace(string(b)), "issue edit 42 --add-label ard:approved -R o/r"; got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestApproveIssueNoGH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := newLiveT(t).ApproveIssue(context.Background(), "o/r", 42); !errors.Is(err, ErrGHUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestApproveIssueNotLoggedIn(t *testing.T) {
	fakeGH(t, 1, "To get started with GitHub CLI, please run:  gh auth login")
	if err := newLiveT(t).ApproveIssue(context.Background(), "o/r", 42); !errors.Is(err, ErrGHUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestApproveIssueOtherFailure(t *testing.T) {
	fakeGH(t, 1, "boom")
	err := newLiveT(t).ApproveIssue(context.Background(), "o/r", 42)
	if err == nil || errors.Is(err, ErrGHUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestApproveIssueBadInput(t *testing.T) {
	argv := fakeGH(t, 0, "")
	l := newLiveT(t)
	for _, c := range []struct {
		repo string
		n    int
	}{{"o/r", 0}, {"o/r", -1}, {"o/r;rm", 5}, {"o", 5}, {"", 5}} {
		if err := l.ApproveIssue(context.Background(), c.repo, c.n); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
	if _, err := os.Stat(argv); err == nil {
		t.Fatal("gh ran on bad input")
	}
}

// The App client must never be touched: failClient fails the test on any write.
func TestApproveNeverUsesAppClient(t *testing.T) {
	fakeGH(t, 0, "")
	if err := newLiveT(t).ApproveIssue(context.Background(), "o/r", 7); err != nil {
		t.Fatal(err)
	}
}

func TestDryRunApproveRecords(t *testing.T) {
	argv := fakeGH(t, 0, "")
	led := filepath.Join(t.TempDir(), "ledger.jsonl")
	d, err := NewDryRun(DryRunOptions{Log: slog.New(slog.DiscardHandler), LedgerPath: led})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.ApproveIssue(context.Background(), "o/r", 42); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(led)
	if !strings.Contains(string(b), `"op":"ApproveIssue"`) || !strings.Contains(string(b), "ard:approved") {
		t.Fatalf("ledger = %s", b)
	}
	if _, err := os.Stat(argv); err == nil {
		t.Fatal("gh ran in dry-run")
	}
}
