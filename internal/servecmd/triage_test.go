package servecmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/triage"
)

// field reads an unexported struct field, to prove the triage loop got the
// process-wide parts rather than copies.
func field(t *testing.T, v any, path ...string) any {
	t.Helper()
	rv := reflect.ValueOf(v)
	for _, name := range path {
		for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
			rv = rv.Elem()
		}
		rv = rv.FieldByName(name)
		if !rv.IsValid() {
			t.Fatalf("no field %s", name)
		}
		rv = reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem()
	}
	return rv.Interface()
}

// The triage loop must use the one executor and the one Finisher (and through
// it the one Closer) that the Reaper uses.
func TestTriageLoopSharesParts(t *testing.T) {
	w := newTestWiring(t)
	l := triageLoop(w)
	if l == nil {
		t.Fatal("no triage loop")
	}
	tl, ok := l.(*triage.Loop)
	if !ok {
		t.Fatalf("loop is %T", l)
	}
	ex, _ := w.newExecutor()
	fin, _ := w.finisher()
	if got := field(t, tl, "d", "Executor"); got != any(ex) {
		t.Errorf("executor %v is not the shared one", got)
	}
	if got := field(t, tl, "d", "Finisher"); got != any(fin) {
		t.Errorf("finisher %v is not the shared one", got)
	}
	if fin.Closer.(*closeout.Closer) != w.closer() {
		t.Error("the Finisher does not hold the shared Closer")
	}
}

// The shared Closer redacts the model credential from a committed log. This
// runs a real close-out against a local bare remote.
func TestSharedCloserRedactsOAuthToken(t *testing.T) {
	const tok = "ard-oauth-test-value-0123456789abcdef"
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", tok)
	w := newTestWiring(t)
	bare, clone := gittest.NewRemote(t)
	gh := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", ID: 4242, Kind: github.AuthApp}))
	w.Reader = gh
	w.Effects = effects.NewLive(gh, gitx.New(""), w.Log)
	w.Root, w.RemoteURL = clone, "file://"+bare
	// The committer refuses a staging path that involves a symlink (/var on macOS).
	stage, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w.StateDir = stage
	w.cfg.Store(&config.Resolved{Roles: []config.Role{{Kind: config.KindTriage, Name: "lead"}}})
	n := gh.SeedIssue(github.Issue{Title: "t", Labels: []string{labels.Pending, labels.Running(labels.StageTriage, "lead")}})

	sj, err := w.Store.CreateJob(context.Background(), state.NewJob{Stage: "triage", Role: "lead", Issue: n})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "triage-lead.log")
	if err := os.WriteFile(logPath, []byte("agent said "+tok+"\nbye\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := w.closer().Run(context.Background(), closeout.Job{
		ID: sj.ID, Issue: n, Stage: "triage", Role: "lead", ExitCode: 1, LogPath: logPath, Worktree: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != closeout.OutcomeFailed {
		t.Fatalf("outcome %s", out)
	}
	files := gittest.Run(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "logs/") {
		t.Fatalf("no log was committed:\n%s", files)
	}
	for _, f := range strings.Fields(files) {
		if got := gittest.Run(t, bare, "show", "main:"+f); strings.Contains(got, tok) {
			t.Fatalf("%s in the commit holds the token", f)
		}
	}
}
