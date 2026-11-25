package initcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/configcmd"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type harness struct {
	bare, clone string
	fake        *fake.Fake
	opened      bool
	dry         bool
	ledger      string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	bare, clone := gittest.NewRemote(t)
	return &harness{bare: bare, clone: clone, fake: fake.New()}
}

func (h *harness) env(dry bool) (*cli.Env, cli.InitOpts, *bytes.Buffer) {
	var out bytes.Buffer
	h.dry = dry
	return &cli.Env{
		Globals: cli.Globals{ConfigPath: filepath.Join(h.clone, ".ard", "config.yaml"), DryRun: dry},
		Log:     quiet, Stdout: &out, Stderr: &bytes.Buffer{},
	}, cli.InitOpts{Version: "v-test"}, &out
}

func (h *harness) open(t *testing.T) func(context.Context, string) (deps, error) {
	return func(context.Context, string) (deps, error) {
		h.opened = true
		d := deps{Repo: github.Repo{Owner: "o", Name: "r"}, Reader: h.fake, RemoteURL: "file://" + h.bare}
		if h.dry {
			h.ledger = filepath.Join(t.TempDir(), "effects.jsonl")
			dr, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: h.ledger, Staged: gitx.New("").StagedPaths})
			if err != nil {
				return deps{}, err
			}
			d.Effects, d.Close = dr, dr.Close
		} else {
			d.Effects = effects.NewLive(h.fake, gitx.New(""), quiet)
		}
		return d, nil
	}
}

func (h *harness) run(t *testing.T, dry bool) (string, error) {
	t.Helper()
	e, o, out := h.env(dry)
	err := run(context.Background(), e, o, h.open(t))
	return out.String(), err
}

// commitMain adds files to main and pushes it.
func (h *harness) commitMain(t *testing.T, files map[string]string) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(h.clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if b := filepath.Base(rel); b == "scan" || b == "aid" {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, h.clone, "add", "-A")
	gittest.Run(t, h.clone, "commit", "-q", "-m", "seed")
	gittest.Run(t, h.clone, "push", "-q", "origin", "main")
}

func (h *harness) branches(t *testing.T) string {
	return strings.TrimSpace(gittest.Run(t, h.bare, "branch", "--list"))
}

func (h *harness) tree(t *testing.T, ref string) []string {
	t.Helper()
	out := strings.Fields(gittest.Run(t, h.bare, "ls-tree", "-r", "--name-only", ref))
	sort.Strings(out)
	return out
}

// snapshot maps every non-.git file of dir to its contents.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			m[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		m[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var starterTree = []string{".ard/PRODUCT.md", ".ard/TECH_STACK.md", ".ard/VISION.md", ".ard/config.yaml", ".ard/reviewers/eng-review/prompt.txt", ".ard/reviewers/license-review/allowlist.txt", ".ard/reviewers/license-review/prompt.txt", ".ard/reviewers/product-manager-review/prompt.txt", ".ard/reviewers/security-review/Dockerfile", ".ard/reviewers/security-review/aid", ".ard/reviewers/security-review/normalize.jq", ".ard/reviewers/security-review/prompt.txt", ".ard/reviewers/security-review/rules/security.yml", ".ard/scanners/cve/Dockerfile", ".ard/scanners/cve/merge.jq", ".ard/scanners/cve/scan", ".ard/scanners/docs/Dockerfile", ".ard/scanners/docs/docscan.go", ".ard/scanners/docs/scan", ".ard/scanners/error-codes/Dockerfile", ".ard/scanners/error-codes/errcodes.jq", ".ard/scanners/error-codes/scan", ".ard/scanners/lint/Dockerfile", ".ard/scanners/lint/normalize.jq", ".ard/scanners/lint/scan", ".ard/scanners/lts/Dockerfile", ".ard/scanners/lts/eol.jq", ".ard/scanners/lts/products.json", ".ard/scanners/lts/refs.jq", ".ard/scanners/lts/scan", ".ard/scanners/missing-tests/Dockerfile", ".ard/scanners/missing-tests/missingtests.go", ".ard/scanners/missing-tests/scan", ".ard/scanners/trufflehog-history/Dockerfile", ".ard/scanners/trufflehog-history/exclude-paths", ".ard/scanners/trufflehog-history/findings.jq", ".ard/scanners/trufflehog-history/history", ".ard/scanners/trufflehog-history/scan", ".ard/scanners/trufflehog/Dockerfile", ".ard/scanners/trufflehog/exclude-paths", ".ard/scanners/trufflehog/findings.jq", ".ard/scanners/trufflehog/scan", ".ard/triage/architect/prompt.txt", ".ard/triage/designer/prompt.txt", ".ard/triage/documentation-planner/prompt.txt", ".ard/triage/engineer/prompt.txt", ".ard/triage/lead/prompt.txt", ".ard/triage/product-manager/prompt.txt", ".ard/workers/engineer/prompt.txt"}

func TestInitOpensPR(t *testing.T) {
	h := newHarness(t)
	before := snapshot(t, h.clone)
	out, err := h.run(t, false)
	if err != nil {
		t.Fatal(err)
	}
	wantTree := append(slices.Clone(starterTree), LockPath)
	slices.Sort(wantTree)
	if got := h.tree(t, "ard-init"); !reflect.DeepEqual(got, wantTree) {
		t.Errorf("ard-init tree = %v, want %v", got, wantTree)
	}
	if got := strings.TrimSpace(gittest.Run(t, h.bare, "ls-tree", "-r", "ard-init", "--", ".ard/scanners/cve/scan")); !strings.HasPrefix(got, "100755 ") {
		t.Errorf("scan = %q, want mode 100755", got)
	}
	if got := strings.TrimSpace(gittest.Run(t, h.bare, "ls-tree", "-r", "ard-init", "--", ".ard/scanners/docs/scan")); !strings.HasPrefix(got, "100755 ") {
		t.Errorf("docs scan = %q, want mode 100755", got)
	}
	if got := strings.TrimSpace(gittest.Run(t, h.bare, "ls-tree", "-r", "ard-init", "--", ".ard/scanners/lint/scan")); !strings.HasPrefix(got, "100755 ") {
		t.Errorf("lint scan = %q, want mode 100755", got)
	}
	if !reflect.DeepEqual(snapshot(t, h.clone), before) {
		t.Error("the working tree changed")
	}
	if n := strings.TrimSpace(gittest.Run(t, h.bare, "rev-list", "--count", "main")); n != "1" {
		t.Errorf("main has %s commits, want 1", n)
	}

	// Done when: ard config check passes on the proposed branch.
	gittest.Run(t, h.clone, "fetch", "-q", "origin", "+refs/heads/ard-init:refs/ard/upstream/ard-init")
	ce := &cli.Env{Globals: cli.Globals{ConfigPath: filepath.Join(h.clone, ".ard", "config.yaml")}, Log: quiet, Stdout: io.Discard, Stderr: &bytes.Buffer{}}
	if err := configcmd.Check(context.Background(), ce, cli.ConfigCheckOpts{Ref: "refs/ard/upstream/ard-init"}); err != nil {
		t.Fatalf("config check on ard-init: %v", err)
	}
	cres, err := loadStarter(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cres.Intake != (config.Intake{Mode: config.IntakeLabel}) {
		t.Errorf("intake = %+v", cres.Intake)
	}
	cve, _ := cres.Role(config.KindScanner, "cve")
	if cve.Every != 6*time.Hour || cve.MaxOpen != 10 || cve.GroupSize != 1 || !cve.Autoclose {
		t.Errorf("cve = %+v", cve)
	}
	docs, _ := cres.Role(config.KindScanner, "docs")
	if docs.Every != 72*time.Hour || docs.MaxOpen != 3 || docs.GroupSize != 10 || !docs.Autoclose {
		t.Errorf("docs = %+v", docs)
	}
	lint, ok := cres.Role(config.KindScanner, "lint")
	if !ok || lint.Every != 24*time.Hour || lint.MaxOpen != 3 || lint.GroupSize != 20 || !lint.Autoclose {
		t.Errorf("lint = %+v", lint)
	}

	prs, err := h.fake.ListPRs(context.Background(), github.ListPRsOpts{})
	if err != nil || len(prs) != 1 {
		t.Fatalf("prs = %v, %v", prs, err)
	}
	pr := prs[0]
	if pr.HeadRef != "ard-init" || pr.BaseRef != "main" || pr.Draft {
		t.Errorf("pr = %+v", pr)
	}
	for _, s := range []string{"bot-commits.yml", "ARD_BOT_LOGIN", "Squash and merge", ".ard/scanners/cve/scan", ".ard/scanners/error-codes/Dockerfile", ".ard/scanners/error-codes/errcodes.jq", ".ard/scanners/error-codes/scan", ".ard/scanners/trufflehog/scan", "delete `.ard/scanners/trufflehog/`", ".ard/triage/lead/prompt.txt", ".ard/triage/engineer/prompt.txt", ".ard/workers/engineer/prompt.txt", ".ard/reviewers/eng-review/prompt.txt", ".ard/reviewers/security-review/prompt.txt", ".ard/reviewers/product-manager-review/prompt.txt"} {
		if !strings.Contains(pr.Body, s) {
			t.Errorf("PR body lacks %q:\n%s", s, pr.Body)
		}
	}
	if strings.Contains(pr.Body, "@") || strings.Contains(strings.ToLower(pr.Body), "closes") {
		t.Errorf("PR body has a mention or closing keyword:\n%s", pr.Body)
	}
	have, err := h.fake.ListLabels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range have {
		found = found || l.Name == labels.Scanner("cve")
	}
	if !found {
		t.Errorf("label %s missing: %v", labels.Scanner("cve"), have)
	}
	foundDocs := false
	for _, l := range have {
		foundDocs = foundDocs || l.Name == labels.Scanner("docs")
	}
	if !foundDocs {
		t.Errorf("label %s missing: %v", labels.Scanner("docs"), have)
	}
	names := map[string]bool{}
	for _, l := range have {
		names[l.Name] = true
	}
	if !names[labels.Scanner("lint")] {
		t.Errorf("label %s missing: %v", labels.Scanner("lint"), have)
	}
	if !strings.Contains(pr.Body, "The lint scanner fails until you commit `.ard/scanners/lint/baseline`") {
		t.Errorf("PR body lacks the lint baseline bullet:\n%s", pr.Body)
	}
	for _, want := range []string{labels.Running(labels.StageTriage, "lead"), labels.Done(labels.StageTriage, "lead"), "ard:triage:engineer", labels.Running(labels.StageTriage, "engineer"), labels.Done(labels.StageTriage, "engineer"), labels.Request(labels.StageWorker, "engineer"), labels.Running(labels.StageWorker, "engineer"), labels.Done(labels.StageWorker, "engineer"), labels.Request(labels.StageReview, "eng-review"), labels.Running(labels.StageReview, "eng-review"), labels.Done(labels.StageReview, "eng-review"), labels.Changes("eng-review"), labels.Request(labels.StageReview, "security-review"), labels.Running(labels.StageReview, "security-review"), labels.Done(labels.StageReview, "security-review"), labels.Changes("security-review"), labels.Request(labels.StageReview, "product-manager-review"), labels.Running(labels.StageReview, "product-manager-review"), labels.Done(labels.StageReview, "product-manager-review"), labels.Changes("product-manager-review")} {
		if !names[want] {
			t.Errorf("label %s missing", want)
		}
	}
	if names["ard:triage:lead"] {
		t.Error("label ard:triage:lead must not exist: the lead's request label is ard:pending")
	}
	for _, s := range []string{"add  .ard/scanners/cve/scan (0755)", "add  .ard/config.yaml", "opened PR #"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout lacks %q:\n%s", s, out)
		}
	}
}

// loadStarter loads the starter tree laid over base (nil = an empty repo).
func loadStarter(t *testing.T, base fs.FS) (*config.Resolved, error) {
	t.Helper()
	files, err := starter()
	if err != nil {
		t.Fatal(err)
	}
	if base == nil {
		base = fstest.MapFS{}
	}
	m, err := overlay(base, files)
	if err != nil {
		t.Fatal(err)
	}
	res, err := config.LoadFS(m, "/repo")
	return res, err
}

func TestInitDryRun(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, true)
	if err != nil {
		t.Fatal(err)
	}
	if b := h.branches(t); strings.Contains(b, "ard-init") {
		t.Errorf("dry-run pushed a branch: %s", b)
	}
	if w := h.fake.Writes(); len(w) != 0 {
		t.Errorf("dry-run made %d GitHub writes: %v", len(w), w)
	}
	for _, s := range []string{"would add .ard/config.yaml", "dry-run: would open a PR from ard-init", "would create"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout lacks %q:\n%s", s, out)
		}
	}
	b, err := os.ReadFile(h.ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{`"op":"Commit"`, `"op":"Push"`, `"op":"CreatePR"`} {
		if strings.Count(string(b), op) != 1 {
			t.Errorf("ledger should have one %s:\n%s", op, b)
		}
	}
}

func TestInitRefusesWhenBranchExists(t *testing.T) {
	for _, withPR := range []bool{true, false} {
		name := "no PR"
		if withPR {
			name = "open PR"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			gittest.Run(t, h.bare, "branch", "ard-init", "main")
			var prNum int
			if withPR {
				prNum = h.fake.SeedPR(github.PullRequest{Title: "init", HeadRef: "ard-init", BaseRef: "main", State: github.StateOpen})
			}
			tipBefore := strings.TrimSpace(gittest.Run(t, h.bare, "rev-parse", "ard-init"))
			_, err := h.run(t, false)
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("err = %v, want 'already exists'", err)
			}
			if got := strings.Contains(err.Error(), "open PR #"); got != withPR {
				t.Errorf("err = %v, names a PR = %v, want %v", err, got, withPR)
			}
			if withPR && !strings.Contains(err.Error(), "#"+strconv.Itoa(prNum)) {
				t.Errorf("err = %v, want PR #%d", err, prNum)
			}
			if got := strings.TrimSpace(gittest.Run(t, h.bare, "rev-parse", "ard-init")); got != tipBefore {
				t.Error("ard-init moved")
			}
			if w := h.fake.Writes(); len(w) != 0 {
				t.Errorf("writes = %v, want none (no label sync, no PR)", w)
			}
		})
	}
}

func TestInitNothingToPropose(t *testing.T) {
	h := newHarness(t)
	files, err := starter()
	if err != nil {
		t.Fatal(err)
	}
	seed := map[string]string{}
	for _, f := range files {
		seed[f.Path] = string(f.Data)
	}
	h.commitMain(t, seed)
	out, err := h.run(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to propose: main already has the starter files") {
		t.Errorf("stdout:\n%s", out)
	}
	if strings.Contains(h.branches(t), "ard-init") {
		t.Error("branch created")
	}
	if prs, _ := h.fake.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateAll}); len(prs) != 0 {
		t.Errorf("prs = %v", prs)
	}
	have, _ := h.fake.ListLabels(context.Background())
	if len(have) == 0 {
		t.Error("labels were not synced")
	}
}

func TestInitInvalidResultWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.commitMain(t, map[string]string{".ard/triage/foo/.gitkeep": ""})
	_, err := h.run(t, false)
	if err == nil || !strings.Contains(err.Error(), "invalid") || !strings.Contains(err.Error(), "foo") {
		t.Fatalf("err = %v, want an invalid-configuration error naming foo", err)
	}
	if strings.Contains(h.branches(t), "ard-init") || len(h.fake.Writes()) != 0 {
		t.Errorf("something was pushed or synced: %s %v", h.branches(t), h.fake.Writes())
	}
}

func TestInitMissingPieces(t *testing.T) {
	ctx := context.Background()
	t.Run("open fails", func(t *testing.T) {
		h := newHarness(t)
		e, o, _ := h.env(false)
		before := snapshot(t, h.clone)
		err := run(ctx, e, o, func(context.Context, string) (deps, error) { return deps{}, errors.New("ard init: no credentials") })
		if err == nil || !strings.Contains(err.Error(), "no credentials") {
			t.Fatalf("err = %v", err)
		}
		if !reflect.DeepEqual(snapshot(t, h.clone), before) || strings.Contains(h.branches(t), "ard-init") {
			t.Error("something was written")
		}
		if refs := gittest.Run(t, h.clone, "for-each-ref", "refs/ard/"); refs != "" {
			t.Errorf("fetched before failing: %s", refs)
		}
	})
	t.Run("no repo and no origin", func(t *testing.T) {
		_, clone := gittest.NewRemote(t)
		gittest.Run(t, clone, "remote", "remove", "origin")
		if _, err := resolveRepo(ctx, "", clone); err == nil || !strings.Contains(err.Error(), "--repo owner/repo is required") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("origin token is not echoed", func(t *testing.T) {
		_, clone := gittest.NewRemote(t)
		gittest.Run(t, clone, "remote", "set-url", "origin", (&url.URL{Scheme: "https", User: url.UserPassword("x", "secret"), Host: "gitlab.com", Path: "/a/b.git"}).String())
		if _, err := resolveRepo(ctx, "", clone); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("origin fallback and flag", func(t *testing.T) {
		_, clone := gittest.NewRemote(t)
		gittest.Run(t, clone, "remote", "set-url", "origin", "https://github.com/acme/widgets.git")
		if got, err := resolveRepo(ctx, "", clone); err != nil || got != (github.Repo{Owner: "acme", Name: "widgets"}) {
			t.Errorf("got %v, %v", got, err)
		}
		if got, err := resolveRepo(ctx, "o/r", clone); err != nil || got != (github.Repo{Owner: "o", Name: "r"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("checkAuth", func(t *testing.T) {
		tests := []struct {
			kind    github.AuthKind
			dry     bool
			wantErr bool
		}{
			{github.AuthToken, false, true}, {github.AuthToken, true, false},
			{github.AuthApp, false, false}, {github.AuthApp, true, false},
		}
		for _, tt := range tests {
			if err := checkAuth(tt.kind, tt.dry); (err != nil) != tt.wantErr {
				t.Errorf("checkAuth(%v, %v) = %v", tt.kind, tt.dry, err)
			}
		}
	})
	t.Run("no .git at root", func(t *testing.T) {
		dir := t.TempDir()
		e := &cli.Env{Globals: cli.Globals{ConfigPath: filepath.Join(dir, ".ard", "config.yaml")}, Log: quiet, Stdout: io.Discard, Stderr: io.Discard}
		called := false
		err := run(ctx, e, cli.InitOpts{}, func(context.Context, string) (deps, error) { called = true; return deps{}, nil })
		if err == nil || !strings.Contains(err.Error(), "not the root of a git repository") || called {
			t.Errorf("err = %v, open called = %v", err, called)
		}
	})
	t.Run("no main branch", func(t *testing.T) {
		h := newHarness(t)
		gittest.Run(t, h.bare, "branch", "-m", "main", "trunk")
		gittest.Run(t, h.bare, "symbolic-ref", "HEAD", "refs/heads/trunk")
		_, err := h.run(t, false)
		if err == nil || !strings.Contains(err.Error(), "has no main branch") {
			t.Errorf("err = %v", err)
		}
		if len(h.fake.Writes()) != 0 {
			t.Error("wrote to GitHub")
		}
	})
}

func TestInitCreatePRFailsAfterPush(t *testing.T) {
	h := newHarness(t)
	h.fake.FailNext("CreatePR", errors.New("boom"))
	_, err := h.run(t, false)
	if err == nil || !strings.Contains(err.Error(), "pushed but opening the PR failed") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(h.branches(t), "ard-init") {
		t.Error("branch should have been pushed")
	}
}

func TestInitLabelSyncFailureStopsBeforePush(t *testing.T) {
	h := newHarness(t)
	h.fake.FailNext("CreateLabel", errors.New("denied"))
	_, err := h.run(t, false)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(h.branches(t), "ard-init") {
		t.Error("pushed after a label failure")
	}
}

func TestDefaultProtectedPaths(t *testing.T) {
	res, err := loadStarter(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ProtectedPaths) != 21 || res.ProtectedPaths[0] != ".github/" || res.ProtectedPaths[2] != ".ard/ard-init.lock" || res.ProtectedPaths[7] != ".ard/PRODUCT.md" || res.ProtectedPaths[9] != ".ard/TECH_STACK.md" || res.ProtectedPaths[16] != ".mcp.json" || res.ProtectedPaths[20] != "**/.golangci.json" {
		t.Errorf("ProtectedPaths = %v", res.ProtectedPaths)
	}
	yes := []string{".github/workflows/ci.yml", ".ard/config.yaml", ".ard/triage/x/prompt.txt", "CODEOWNERS", "docs/CODEOWNERS",
		".github/CODEOWNERS", "CLAUDE.md", "pkg/CLAUDE.md", "AGENTS.md", "a/b/AGENTS.md", ".claude/settings.json", ".mcp.json", ".golangci.yml", "sub/.golangci.toml",
		".ard/PRODUCT.md", ".ard/VISION.md", ".ard/TECH_STACK.md", ".ard/ard-init.lock"}
	no := []string{".ard/NOTES.md", "docs/PRODUCT.md", ".ard/12/issue.md", "main.go", "docs/CODEOWNERS.md", "pkg/.mcp.json"}
	for _, p := range yes {
		if !res.Protected(p) {
			t.Errorf("Protected(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if res.Protected(p) {
			t.Errorf("Protected(%q) = true, want false", p)
		}
	}
}

func TestInitContextFiles(t *testing.T) {
	has := func(tree []string, p string) bool { return slices.Contains(tree, p) }
	// all present with different content: none proposed, none overwritten
	h := newHarness(t)
	h.commitMain(t, map[string]string{".ard/PRODUCT.md": "mine\n", ".ard/VISION.md": "mine\n", ".ard/TECH_STACK.md": "mine\n"})
	if _, err := h.run(t, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range ContextPaths() {
		if !has(h.tree(t, "main"), p) {
			t.Fatalf("%s missing on main", p)
		}
	}
	if got := gittest.Run(t, h.bare, "diff", "--name-only", "main", "ard-init"); strings.Contains(got, "PRODUCT.md") || strings.Contains(got, "VISION.md") || strings.Contains(got, "TECH_STACK.md") {
		t.Errorf("ard-init changes context files:\n%s", got)
	}
	if got := gittest.Run(t, h.bare, "show", "ard-init:.ard/PRODUCT.md"); got != "mine\n" {
		t.Errorf("PRODUCT.md = %q", got)
	}

	// one present, two absent: only the two are proposed
	h = newHarness(t)
	h.commitMain(t, map[string]string{".ard/VISION.md": "mine\n"})
	if _, err := h.run(t, false); err != nil {
		t.Fatal(err)
	}
	if got := gittest.Run(t, h.bare, "diff", "--name-only", "main", "ard-init"); strings.Contains(got, "VISION.md") || !strings.Contains(got, ".ard/PRODUCT.md") || !strings.Contains(got, ".ard/TECH_STACK.md") {
		t.Errorf("diff:\n%s", got)
	}
}

func TestInitBodyProtectedPathsHint(t *testing.T) {
	res, err := loadStarter(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := initBody(Result{}, res)
	if !strings.Contains(body, "merge.protected_paths") || !strings.Contains(body, "`.ard/TECH_STACK.md`") {
		t.Errorf("body lacks the protected_paths instruction:\n%s", body)
	}
}

func TestInitLockEndToEnd(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, false); err != nil {
		t.Fatal(err)
	}
	lockText := gittest.Run(t, h.bare, "show", "ard-init:"+LockPath)
	lock, warns := ParseLock([]byte(lockText))
	files, _ := starter()
	if len(warns) != 0 || lock.Version != "v-test" || len(lock.Files) != len(files) {
		t.Fatalf("lock: version %q, %d entries (want %d), warns %v", lock.Version, len(lock.Files), len(files), warns)
	}

	// Rerun against a main that has the merged tree: nothing to propose.
	seed := map[string]string{LockPath: lockText}
	for _, f := range files {
		seed[f.Path] = string(f.Data)
	}
	h2 := newHarness(t)
	h2.commitMain(t, seed)
	if out, err := h2.run(t, false); err != nil || !strings.Contains(out, "nothing to propose") {
		t.Fatalf("rerun: %q %v", out, err)
	}

	// An edited file stays untouched and its diff is printed; a dropped lock
	// entry is listed; an untouched stale file is updated and re-locked.
	var edited, stale string
	for _, f := range files {
		if f.Path == ".ard/config.yaml" || slices.Contains(ContextPaths(), f.Path) || f.Path == ".ard/scanners/cve/scan" {
			continue
		}
		switch {
		case edited == "":
			edited = f.Path
		case stale == "":
			stale = f.Path
		}
	}
	old := lock
	old.Files = map[string]string{}
	for k, v := range lock.Files {
		old.Files[k] = v
	}
	old.Files[".ard/triage/gone/prompt.txt"] = hashOf([]byte("g"))
	old.Files[stale] = hashOf([]byte("previous starter\n"))
	oldText, _ := old.Marshal()
	seed[LockPath] = string(oldText)
	seed[edited] = "hand edited\n"
	seed[stale] = "previous starter\n"
	h3 := newHarness(t)
	h3.commitMain(t, seed)
	out, err := h3.run(t, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"update  " + stale, "edited " + edited + ": left alone", "+hand", ".ard/triage/gone/prompt.txt: no longer in the starter"} {
		if !strings.Contains(out, w) && !strings.Contains(out, strings.Replace(w, "+hand", "-hand", 1)) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	if got := gittest.Run(t, h3.bare, "show", "ard-init:"+edited); got != "hand edited\n" {
		t.Errorf("edited file changed: %q", got)
	}
	if got := gittest.Run(t, h3.bare, "diff", "--name-only", "main", "ard-init"); !strings.Contains(got, stale) || strings.Contains(got, edited) || strings.Contains(got, "gone") {
		t.Errorf("diff:\n%s", got)
	}
	nl, _ := ParseLock([]byte(gittest.Run(t, h3.bare, "show", "ard-init:"+LockPath)))
	if nl.Files[stale] != hashOf(starterData(t, stale)) || nl.Files[".ard/triage/gone/prompt.txt"] == "" {
		t.Errorf("new lock: %v", nl.Files)
	}
}

func starterData(t *testing.T, p string) []byte {
	t.Helper()
	files, _ := starter()
	for _, f := range files {
		if f.Path == p {
			return f.Data
		}
	}
	t.Fatalf("no starter file %s", p)
	return nil
}
