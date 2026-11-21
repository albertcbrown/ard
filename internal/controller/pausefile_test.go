package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/repopath"
)

func TestPauseFilePath(t *testing.T) {
	got, err := PauseFilePath("/h", "Own", "ReP")
	if want := "/h/.ard/pause/own/rep"; err != nil || got != want {
		t.Errorf("got %q, %v want %q", got, err, want)
	}
	a, _ := PauseFilePath("/h", "a-b", "c")
	b, _ := PauseFilePath("/h", "a", "b-c")
	if a == b {
		t.Errorf("a-b/c and a/b-c collide at %s", a)
	}
	for _, in := range [][2]string{{"", "r"}, {"o", ""}, {"..", "r"}, {"o", ".."}, {"a/b", "c"}} {
		if _, err := PauseFilePath("/h", in[0], in[1]); !errors.Is(err, repopath.ErrInvalid) {
			t.Errorf("PauseFilePath(%q,%q) err = %v, want repopath.ErrInvalid", in[0], in[1], err)
		}
	}
}

func TestPauseGateLocalSources(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string) (pauseFile string) // returns the path given to SetPauseFile
		cfg     *config.Resolved
		label   bool
		want    Reason
		wantErr bool
	}{
		{name: "no file", setup: func(t *testing.T, dir string) string { return filepath.Join(dir, "pause", "o", "r") }, cfg: &config.Resolved{}},
		{name: "a file", setup: func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "pause", "o", "r")
			mustWrite(t, p, "")
			return p
		}, cfg: nil, want: ReasonPauseFile},
		{name: "a directory", setup: func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "pause", "o", "r")
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			return p
		}, cfg: &config.Resolved{}, want: ReasonPauseFile},
		{name: "file wins over config paused", setup: func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "pause", "o", "r")
			mustWrite(t, p, "")
			return p
		}, cfg: &config.Resolved{Paused: true}, want: ReasonPauseFile},
		{name: "parent is a regular file", setup: func(t *testing.T, dir string) string {
			mustWrite(t, filepath.Join(dir, "pause", "o"), "")
			return filepath.Join(dir, "pause", "o", "r")
		}, cfg: &config.Resolved{}, want: ReasonPauseFileUnreadable, wantErr: true},
		{name: "empty path is off", setup: func(t *testing.T, dir string) string { return "" }, cfg: &config.Resolved{}},
		{name: "file beats label", setup: func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "pause", "o", "r")
			mustWrite(t, p, "")
			return p
		}, cfg: &config.Resolved{}, label: true, want: ReasonPauseFile},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New()
			n := 0
			if tc.label {
				n = f.SeedIssue(github.Issue{Labels: []string{labels.Pause}})
			}
			log, _ := newLog()
			g := NewPauseGate(f, log)
			g.SetStatusIssue(n)
			g.SetPauseFile(tc.setup(t, t.TempDir()))
			st := g.Check(ctx, tc.cfg)
			if st.Reason != tc.want || st.Paused != (tc.want != ReasonNone) {
				t.Errorf("got %+v, want reason %q", st, tc.want)
			}
			if (st.Err != nil) != tc.wantErr {
				t.Errorf("Err = %v, want error: %v", st.Err, tc.wantErr)
			}
		})
	}
}

func TestPauseGateLocalConfig(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		cfg  string // "" = no file
		want Reason
	}{
		{"missing file does not pause", "", ReasonNone},
		{"paused true", "paused: true\nmax_pods: 9\n", ReasonLocalConfig},
		{"paused false", "paused: false\n", ReasonNone},
		{"invalid yaml does not pause", "paused: true\n  : : bad", ReasonNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.cfg != "" {
				mustWrite(t, filepath.Join(root, ".ard", "config.yaml"), tc.cfg)
			}
			log, _ := newLog()
			g := NewPauseGate(fake.New(), log)
			g.SetLocalConfig(root)
			// origin's config says not paused; only the local file can pause.
			if st := g.Check(ctx, &config.Resolved{}); st.Reason != tc.want || st.Paused != (tc.want != ReasonNone) {
				t.Errorf("got %+v, want %q", st, tc.want)
			}
		})
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPauseFileStopsClaimingLoopsWithinOnePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pause", "o", "r")
		log, buf := newLog()
		g := NewPauseGate(fake.New(), log)
		g.SetPauseFile(path)
		claim := &testLoop{name: "claim", claims: true}
		reap := &testLoop{name: "reaper"}
		stop := start(t, mustNew(t, Deps{Log: log, Pause: g, Rand: fixedRand(0.5)}, claim, reap))
		time.Sleep(iv * 3)

		mustWrite(t, path, "")
		time.Sleep(iv * 11 / 10) // one interval plus jitter: any tick in flight has ended
		c0, r0 := claim.ticks(), reap.ticks()
		time.Sleep(iv * 6)
		if claim.ticks() != c0 {
			t.Errorf("claiming loop ticked %d times while paused", claim.ticks()-c0)
		}
		if reap.ticks() < r0+5 {
			t.Errorf("non-claiming loop stalled: %d -> %d", r0, reap.ticks())
		}

		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		time.Sleep(iv * 3)
		_ = stop()
		if claim.ticks() <= c0 {
			t.Error("claiming loop did not resume")
		}
		if buf.count("loop.paused") != 1 || buf.count("reason=pause-file") != 1 || buf.count("loop.resumed") != 1 {
			t.Errorf("transition logs not once each:\n%s", buf.String())
		}
	})
}

// A controller whose config has never loaded starts paused instead of
// aborting, runs no loop, and starts once a load succeeds.
func TestStartsPausedWhenInitialConfigFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, buf := newLog()
		var broken atomic.Bool
		broken.Store(true)
		cfgFn := func(context.Context) (*config.Resolved, error) {
			if broken.Load() {
				return nil, errors.New("origin/main: bad yaml")
			}
			return &config.Resolved{}, nil
		}
		claim := &testLoop{name: "claim", claims: true}
		reap := &testLoop{name: "reaper"}
		stop := start(t, mustNew(t, Deps{Log: log, Config: cfgFn, Rand: fixedRand(0.5)}, claim, reap))
		time.Sleep(iv * 3)
		if claim.ticks() != 0 || reap.ticks() != 0 {
			t.Errorf("loops ticked with no config: claim %d, reaper %d", claim.ticks(), reap.ticks())
		}
		broken.Store(false)
		time.Sleep(iv * 3)
		if err := stop(); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if claim.ticks() == 0 || reap.ticks() == 0 {
			t.Errorf("loops did not start once config loaded: claim %d, reaper %d", claim.ticks(), reap.ticks())
		}
		if buf.count("config.initial_failed") != 1 || buf.count("reason=config-invalid") != 1 || buf.count("loop.resumed") != 1 {
			t.Errorf("logs wrong:\n%s", buf.String())
		}
	})
}
