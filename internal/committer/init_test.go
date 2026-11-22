package committer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func TestCheckInitPath(t *testing.T) {
	ok := []string{
		".ard/config.yaml", ".ard/scanners/cve/scan", ".ard/triage/lead/prompt.txt",
		".ard/workers/engineer/sub/x", ".ard/reviewers/eng-review/prompt.txt",
	}
	bad := []string{
		"", ".ard/12/issue.md", ".ard/config.yml", ".ard/scanners/cve", ".ard/scanners/Bad/x",
		".ard/scanners/cve/.git/x", ".ard/scanners/../x", ".ard/scanners/cve/../../x", ".github/workflows/x.yml",
		"CLAUDE.md", "main.go", ".ard/scanners\\cve/x", ".ard/scanners/cve/a\nb", ".ard/scanners/12/x",
		".ard/scanners//cve/x", "/.ard/config.yaml", ".ard/config.yaml/x",
	}
	for _, p := range ok {
		if err := CheckInitPath(p); err != nil {
			t.Errorf("CheckInitPath(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range bad {
		if err := CheckInitPath(p); err == nil {
			t.Errorf("CheckInitPath(%q) = nil, want an error", p)
		}
	}
	// The guard must admit every tree the loader reads, so the two cannot drift.
	for _, sp := range config.SourcePaths() {
		p := sp
		if strings.HasSuffix(sp, "/") || !strings.HasSuffix(sp, ".yaml") {
			p = strings.TrimSuffix(sp, "/") + "/some-role/file.txt"
		}
		if err := CheckInitPath(p); err != nil {
			t.Errorf("CheckInitPath(%q) from config.SourcePaths %q = %v", p, sp, err)
		}
	}
}

var starterFiles = []InitFile{
	{Path: ".ard/config.yaml", Data: []byte("intake: {mode: label}\n")},
	{Path: ".ard/scanners/cve/scan", Data: []byte("#!/bin/sh\n"), Exec: true},
	{Path: ".ard/scanners/cve/merge.jq", Data: []byte(".\n")},
}

func (e *env) init(t *testing.T, fx Effects) *Init {
	t.Helper()
	i, err := NewInit(context.Background(), e.cfg, fx, e.g)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func (e *env) hasBranch(t *testing.T, name string) bool {
	t.Helper()
	return strings.TrimSpace(gittest.Run(t, e.bare, "branch", "--list", name)) != ""
}

func TestInitPropose(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	i := e.init(t, e.live())
	mainBefore := e.head(t, "main")
	base, err := i.Base(ctx)
	if err != nil || base != mainBefore {
		t.Fatalf("Base = %q, %v, want %s", base, err, mainBefore)
	}
	// core.fileMode=false must not change the staged mode.
	gittest.Run(t, e.clone, "config", "core.fileMode", "false")
	res, err := i.Propose(ctx, base, starterFiles)
	if err != nil || res.NoOp || res.SHA == "" {
		t.Fatalf("Propose = %+v, %v", res, err)
	}
	if got := e.head(t, InitBranch); got != res.SHA {
		t.Errorf("remote %s = %s, result %s", InitBranch, got, res.SHA)
	}
	if got := e.head(t, InitBranch+"^"); got != mainBefore {
		t.Errorf("parent = %s, want main's head %s", got, mainBefore)
	}
	if got := e.head(t, "main"); got != mainBefore {
		t.Errorf("main moved: %s -> %s", mainBefore, got)
	}
	names := strings.Fields(gittest.Run(t, e.bare, "diff", "--name-only", "main", InitBranch))
	sort.Strings(names)
	want := []string{".ard/config.yaml", ".ard/scanners/cve/merge.jq", ".ard/scanners/cve/scan"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("diff = %v, want %v", names, want)
	}
	for _, line := range strings.Split(strings.TrimSpace(gittest.Run(t, e.bare, "ls-tree", "-r", InitBranch)), "\n") {
		mode := strings.Fields(line)[0]
		wantMode := "100644"
		if strings.HasSuffix(line, "/scan") {
			wantMode = "100755"
		}
		if mode != wantMode {
			t.Errorf("ls-tree: %s, want mode %s", line, wantMode)
		}
	}
	log := gittest.Run(t, e.bare, "log", "-1", "--format=%an <%ae>|%cn|%B", InitBranch)
	if !strings.HasPrefix(log, "ard[bot] <4242+ard[bot]@users.noreply.github.com>|ard[bot]|"+InitMessage) || strings.Contains(log, "skip ci") {
		t.Errorf("log = %q", log)
	}
	// The worktree is gone.
	if _, err := os.Stat(filepath.Join(e.work, "committer-init")); err == nil {
		t.Error("worktree left behind")
	}
}

func TestInitBranchExists(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	gittest.Run(t, e.bare, "branch", InitBranch, "main")
	i := e.init(t, e.live())
	if _, err := i.Base(ctx); !errors.Is(err, ErrInitExists) {
		t.Fatalf("Base err = %v, want ErrInitExists", err)
	}
	// A racing push (the branch appeared after Base) is refused too, never overwritten.
	gittest.Run(t, e.bare, "branch", "-D", InitBranch)
	base, err := i.Base(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, e.clone, "checkout", "-q", "-b", "other")
	if err := os.WriteFile(filepath.Join(e.clone, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, e.clone, "add", "x")
	gittest.Run(t, e.clone, "commit", "-q", "-m", "x")
	gittest.Run(t, e.clone, "push", "-q", "origin", "other:"+InitBranch)
	theirs := e.head(t, InitBranch)
	if _, err := i.Propose(ctx, base, starterFiles); !errors.Is(err, ErrInitExists) {
		t.Fatalf("Propose err = %v, want ErrInitExists", err)
	}
	if got := e.head(t, InitBranch); got != theirs {
		t.Errorf("branch overwritten: %s -> %s", theirs, got)
	}
}

func TestInitProposeRejects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	i := e.init(t, e.live())
	base, err := i.Base(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		files []InitFile
		guard bool
	}{
		{"issue dir", []InitFile{{Path: ".ard/12/x", Data: []byte("x")}}, true},
		{"code", []InitFile{{Path: "main.go", Data: []byte("x")}}, true},
		{"mixed", append([]InitFile{{Path: ".github/x", Data: []byte("x")}}, starterFiles...), true},
		{"too big", []InitFile{{Path: ".ard/config.yaml", Data: make([]byte, MaxFileBytes+1)}}, true},
		{"none", nil, false},
		{"duplicate", []InitFile{starterFiles[0], starterFiles[0]}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := i.Propose(ctx, base, tt.files)
			var ge *GuardError
			if err == nil || errors.As(err, &ge) != tt.guard {
				t.Fatalf("err = %v, want guard=%v", err, tt.guard)
			}
			if e.hasBranch(t, InitBranch) {
				t.Error("branch pushed")
			}
		})
	}
}

func TestInitProposeNoOpWhenIdentical(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, f := range starterFiles {
		p := filepath.Join(e.clone, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if f.Exec {
			mode = 0o755
		}
		if err := os.WriteFile(p, f.Data, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, e.clone, "add", "-A")
	gittest.Run(t, e.clone, "commit", "-q", "-m", "starter")
	gittest.Run(t, e.clone, "push", "-q", "origin", "main")
	i := e.init(t, e.live())
	base, err := i.Base(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := i.Propose(ctx, base, starterFiles)
	if err != nil || !res.NoOp {
		t.Fatalf("Propose = %+v, %v, want NoOp", res, err)
	}
	if e.hasBranch(t, InitBranch) {
		t.Error("branch created for a no-op")
	}
}

func TestInitDryRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ledger := filepath.Join(t.TempDir(), "effects.jsonl")
	d, err := effects.NewDryRun(effects.DryRunOptions{Log: e.cfg.Log, LedgerPath: ledger, Staged: e.g.StagedPaths})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	i := e.init(t, d)
	base, err := i.Base(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := i.Propose(ctx, base, starterFiles)
	if err != nil || !res.Synthetic {
		t.Fatalf("Propose = %+v, %v, want synthetic", res, err)
	}
	if e.hasBranch(t, InitBranch) {
		t.Error("dry-run pushed the branch")
	}
	b, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"op":"Commit"`); n != 1 {
		t.Errorf("ledger commits = %d, want 1:\n%s", n, b)
	}
	if n := strings.Count(string(b), `"op":"Push"`); n != 1 {
		t.Errorf("ledger pushes = %d, want 1:\n%s", n, b)
	}
}

func TestInitBaseMissingMain(t *testing.T) {
	e := newEnv(t)
	gittest.Run(t, e.bare, "branch", "-m", "main", "trunk")
	gittest.Run(t, e.bare, "symbolic-ref", "HEAD", "refs/heads/trunk")
	i := e.init(t, e.live())
	if _, err := i.Base(context.Background()); !errors.Is(err, ErrBranchGone) {
		t.Errorf("Base err = %v, want ErrBranchGone", err)
	}
}
