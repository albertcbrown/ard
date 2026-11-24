package servecmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/scanner"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// scannerRepo writes a config with a cve scanner (a scan executable) and an
// LLM scanner (a prompt) and returns the config path.
func scannerRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, mode := range map[string]os.FileMode{
		".ard/config.yaml":              0o644,
		".ard/scanners/cve/scan":        0o755,
		".ard/scanners/arch/prompt.txt": 0o644,
	} {
		content := "#!/bin/sh\n"
		if strings.HasSuffix(path, "config.yaml") {
			content = "max_pods: 2\n"
		}
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, ".ard", "config.yaml")
}

func readerFor(f *fake.Fake) readerBuilder {
	return func(context.Context, *cli.Env, github.Repo) (github.Reader, func() error, error) { return f, nil, nil }
}

func seedScannerIssues(f *fake.Fake) {
	body := scanner.RenderBody("", scanner.Parsed{Scanner: "cve", Lines: []scanner.Line{
		{Title: "open one", Fingerprint: "osv:A", Severity: scanner.SevHigh},
		{Checked: true, Title: "ticked", Fingerprint: "osv:B", Severity: scanner.SevLow},
	}}, nil)
	f.SeedIssue(github.Issue{Title: "cve: findings", Body: body, Labels: []string{labels.Scanner("cve")}})
	closedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.SeedIssue(github.Issue{
		Title: "won't fix", State: github.StateClosed, StateReason: github.ReasonNotPlanned, ClosedAt: &closedAt,
		Labels: []string{labels.Scanner("cve")},
		Body:   scanner.RenderBody("", scanner.Parsed{Scanner: "cve", Lines: []scanner.Line{{Title: "wf", Fingerprint: "osv:C", Severity: scanner.SevMedium}}}, nil),
	})
}

func rebuildEnv(t *testing.T, cfg string, dry bool, out *bytes.Buffer) *cli.Env {
	t.Helper()
	return &cli.Env{
		Globals: cli.Globals{Repo: "o/r", ConfigPath: cfg, DryRun: dry},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  out, Stderr: io.Discard,
	}
}

func openScannerDB(t *testing.T, name string) *scannerdb.DB {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir, err := state.Dir(filepath.Join(home, ".ard"), "o/r", false)
	if err != nil {
		t.Fatal(err)
	}
	db, err := scannerdb.Open(context.Background(), dir, name, scannerdb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRebuildStateReplacesTheDatabase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := scannerRepo(t)
	f := fake.New()
	seedScannerIssues(f)
	var out bytes.Buffer
	if err := rebuildState(context.Background(), rebuildEnv(t, cfg, false, &out), "cve", readerFor(f)); err != nil {
		t.Fatal(err)
	}
	db := openScannerDB(t, "cve")
	rows, _ := db.Findings(context.Background())
	sups, _ := db.Suppressions(context.Background())
	if len(rows) != 2 || len(sups) != 1 || sups[0].Fingerprint != "osv:C" || sups[0].Issue != 2 {
		t.Fatalf("rows %+v suppressions %+v", rows, sups)
	}
	if rows[0].Status != scannerdb.StatusOpen || rows[1].Status != scannerdb.StatusGone {
		t.Errorf("rows = %+v", rows)
	}
	for _, want := range []string{"3 added", "osv:A", "replaced state of scanner cve: 2 findings, 1 suppressions"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if w := f.Writes(); len(w) != 0 {
		t.Errorf("rebuild wrote to GitHub: %v", w)
	}

	// A second rebuild finds nothing to change.
	out.Reset()
	if err := rebuildState(context.Background(), rebuildEnv(t, cfg, false, &out), "cve", readerFor(f)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already matches") {
		t.Errorf("output = %s", out.String())
	}
}

func TestRebuildStateDryRunWritesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := scannerRepo(t)
	f := fake.New()
	seedScannerIssues(f)
	var out bytes.Buffer
	if err := rebuildState(context.Background(), rebuildEnv(t, cfg, true, &out), "cve", readerFor(f)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry-run: nothing written") || !strings.Contains(out.String(), "osv:A") {
		t.Errorf("output = %s", out.String())
	}
	home, _ := os.UserHomeDir()
	dir, _ := state.Dir(filepath.Join(home, ".ard"), "o/r", false)
	if _, err := os.Stat(filepath.Join(dir, "cve.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a dry-run created the database: %v", err)
	}
	dryDir, _ := state.Dir(filepath.Join(home, ".ard"), "o/r", true)
	if _, err := os.Stat(dryDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dry-run state directory was touched: %v", err)
	}
}

func TestRebuildStateRefusesWhileServeRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := scannerRepo(t)
	home, _ := os.UserHomeDir()
	lp, err := controller.LockPath(home, "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	leader, err := controller.AcquireLeader(lp)
	if err != nil {
		t.Fatal(err)
	}
	defer leader.Release()
	err = rebuildState(context.Background(), rebuildEnv(t, cfg, false, &bytes.Buffer{}), "cve", readerFor(fake.New()))
	var ee *cli.ExitError
	if !errors.Is(err, controller.ErrLeaderHeld) || !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(err.Error(), "stop ard serve first") {
		t.Errorf("err = %v", err)
	}
}

func TestRebuildStateOnlyForExecutableScanners(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := scannerRepo(t)
	for _, name := range []string{"arch", "stale", "nope"} {
		err := rebuildState(context.Background(), rebuildEnv(t, cfg, false, &bytes.Buffer{}), name, readerFor(fake.New()))
		if err == nil || !strings.Contains(err.Error(), "not a scanner with a scan executable") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDefaultLoopsIncludeTheScannersLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()
	w := &wiring{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Repo: github.Repo{Owner: "o", Name: "r"},
		Root: t.TempDir(), Home: home, Scanners: scannerdb.NewRegistry(t.TempDir(), scannerdb.Options{}),
	}
	defer w.Scanners.Close()
	loops := registerLoops(w)
	if len(loops) != 13 || loops[0].Name() != "scanners" || loops[1].Name() != "stale" || loops[2].Name() != "triage" || loops[3].Name() != "workers" || loops[4].Name() != "reviewers" || loops[5].Name() != "reviewfix" || loops[6].Name() != "merger" || loops[7].Name() != "prwatch" || loops[8].Name() != "janitor" || loops[9].Name() != "reaper" || loops[10].Name() != "costs" || loops[11].Name() != "budget" || loops[12].Name() != "digest" ||
		!loops[0].Claims() || !loops[1].Claims() || !loops[2].Claims() || !loops[3].Claims() || !loops[4].Claims() || !loops[5].Claims() || !loops[6].Claims() || !loops[7].Claims() || !loops[8].Claims() || loops[9].Claims() || loops[10].Claims() {
		t.Fatalf("loops = %v: the scanners, stale, triage, workers, reviewers and merger loops must exist and be skipped while paused, and the Reaper and the costs loop must keep running", loops)
	}
	if _, ok := loops[0].(controller.TickTimeouter); !ok {
		t.Error("the scanners loop must set its own tick timeout")
	}
	w.closeAll() // nothing was built lazily, so nothing to close
}

func TestRebuildStateRefusesLegacyStateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := scannerRepo(t)
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".ard", "state", "o-r", "_controller.db")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := rebuildState(context.Background(), rebuildEnv(t, cfg, false, &bytes.Buffer{}), "cve", readerFor(fake.New()))
	if !errors.Is(err, state.ErrLegacyDir) {
		t.Errorf("err = %v", err)
	}
}
