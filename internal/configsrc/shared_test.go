package configsrc

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// withShared points e at a shared roles remote (another test remote) and
// rebuilds its source.
func (e *env) withShared(shared *env) {
	e.t.Helper()
	s, err := New(Options{
		Git: e.git, RepoDir: e.clone, RemoteURL: e.bare, CacheDir: e.cache,
		SharedDir: filepath.Join(e.t.TempDir(), "shared.git"),
		SharedURL: func(string) string { return shared.bare },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.source = s
}

const rolesCfg = "roles_repo: {repo: org/roles}\n"

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSharedRolesMerged(t *testing.T) {
	shared, e := newEnv(t), newEnv(t)
	shared.push(map[string]string{
		".ard/triage/lead/prompt.txt":      "shared lead\n",
		".ard/workers/engineer/prompt.txt": "shared engineer\n",
	})
	e.push(map[string]string{".ard/config.yaml": rolesCfg})
	e.withShared(shared)
	res, err := e.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.RolesRepo == nil || res.RolesRepo.Repo != "org/roles" || res.RolesSHA == "" {
		t.Fatalf("RolesRepo = %+v, RolesSHA %q", res.RolesRepo, res.RolesSHA)
	}
	if want := filepath.Join(e.cache, res.SHA+"+"+res.RolesSHA[:12]); res.Repo != want {
		t.Errorf("Repo = %s, want %s", res.Repo, want)
	}
	for _, k := range []struct {
		kind config.Kind
		name string
	}{{config.KindTriage, "lead"}, {config.KindWorker, "engineer"}} {
		r, ok := res.Role(k.kind, k.name)
		if !ok || !strings.HasPrefix(readFile(t, filepath.Join(res.Repo, r.PromptPath)), "shared") {
			t.Errorf("role %s = %+v, %v", k.name, r, ok)
		}
	}
}

func TestLocalRoleWins(t *testing.T) {
	shared, e := newEnv(t), newEnv(t)
	shared.push(map[string]string{
		".ard/workers/engineer/prompt.txt": "shared engineer\n",
		".ard/workers/engineer/extra.txt":  "leak\n",
		".ard/workers/engineer/Dockerfile": "FROM scratch\n",
		".ard/triage/lead/prompt.txt":      "shared lead\n",
	})
	e.push(map[string]string{
		".ard/config.yaml":                 rolesCfg,
		".ard/workers/engineer/prompt.txt": "local engineer\n",
	})
	e.withShared(shared)
	res, err := e.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res.Role(config.KindWorker, "engineer")
	if !ok || readFile(t, filepath.Join(res.Repo, r.PromptPath)) != "local engineer\n" {
		t.Fatalf("engineer = %+v, %v", r, ok)
	}
	dir := filepath.Join(res.Repo, ".ard", "workers", "engineer")
	for _, f := range []string{"extra.txt", "Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("shared file %s leaked into the local role: %v", f, err)
		}
	}
	if _, ok := res.Role(config.KindTriage, "lead"); !ok {
		t.Error("shared role lead missing")
	}
}

func TestTwoReposShareOneRoleSet(t *testing.T) {
	shared := newEnv(t)
	shared.push(map[string]string{
		".ard/triage/lead/prompt.txt":      "shared lead\n",
		".ard/workers/engineer/prompt.txt": "shared engineer\n",
	})
	a, b := newEnv(t), newEnv(t)
	a.push(map[string]string{".ard/config.yaml": rolesCfg})
	b.push(map[string]string{
		".ard/config.yaml":                 rolesCfg,
		".ard/workers/engineer/prompt.txt": "b engineer\n",
	})
	a.withShared(shared)
	b.withShared(shared)
	ra, err := a.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rb, err := b.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prompt := func(res *config.Resolved, k config.Kind, n string) string {
		r, ok := res.Role(k, n)
		if !ok {
			t.Fatalf("role %s missing", n)
		}
		return readFile(t, filepath.Join(res.Repo, r.PromptPath))
	}
	if prompt(ra, config.KindTriage, "lead") != "shared lead\n" || prompt(rb, config.KindTriage, "lead") != "shared lead\n" {
		t.Error("both repos should get the shared lead")
	}
	if prompt(ra, config.KindWorker, "engineer") != "shared engineer\n" || prompt(rb, config.KindWorker, "engineer") != "b engineer\n" {
		t.Error("b should override engineer, a should not")
	}
}

func TestSharedRefMoves(t *testing.T) {
	ctx := context.Background()
	shared, e := newEnv(t), newEnv(t)
	shared.push(map[string]string{".ard/triage/lead/prompt.txt": "v1\n"})
	first := strings.TrimSpace(gittest.Run(t, shared.bare, "rev-parse", "main"))

	e.push(map[string]string{".ard/config.yaml": rolesCfg})
	pinned := newEnv(t)
	pinned.push(map[string]string{".ard/config.yaml": "roles_repo: {repo: org/roles, ref: " + first + "}\n"})
	e.withShared(shared)
	pinned.withShared(shared)

	r1, err := e.source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := pinned.source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	shared.push(map[string]string{".ard/triage/lead/prompt.txt": "v2\n"})
	r2, err := e.source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r2.SHA != r1.SHA || r2.RolesSHA == r1.RolesSHA || r2.Repo == r1.Repo {
		t.Errorf("branch move: SHA %s/%s RolesSHA %s/%s", r1.SHA, r2.SHA, r1.RolesSHA, r2.RolesSHA)
	}
	lead, _ := r2.Role(config.KindTriage, "lead")
	if readFile(t, filepath.Join(r2.Repo, lead.PromptPath)) != "v2\n" {
		t.Error("new snapshot lacks the moved prompt")
	}
	p2, err := pinned.source.Load(ctx)
	if err != nil || p2 != p1 || p2.RolesSHA != first {
		t.Errorf("full-SHA pin changed: %+v, %v", p2, err)
	}
}

func TestSharedFailures(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		cfg    string
		shared map[string]string
		url    func(*env) string
	}{
		{"unknown ref", "roles_repo: {repo: org/roles, ref: nope}\n", map[string]string{".ard/triage/lead/prompt.txt": "x\n"}, nil},
		{"unreachable", rolesCfg, nil, func(*env) string { return filepath.Join(t.TempDir(), "missing.git") }},
		{"invalid shared role", rolesCfg, map[string]string{".ard/triage/lead/prompt.txt": ""}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shared, e := newEnv(t), newEnv(t)
			if tc.shared != nil {
				shared.push(tc.shared)
			}
			e.push(map[string]string{".ard/config.yaml": tc.cfg})
			e.withShared(shared)
			if tc.url != nil {
				e.source.o.SharedURL = func(string) string { return tc.url(shared) }
			}
			if res, err := e.source.Load(ctx); err == nil || res != nil {
				t.Fatalf("Load = %+v, %v; want error", res, err)
			}
			ents, _ := os.ReadDir(e.cache)
			if len(ents) != 0 {
				t.Errorf("cache not empty after failure: %v", ents)
			}
		})
	}
}

func TestSharedRepoWithoutRoles(t *testing.T) {
	shared, e := newEnv(t), newEnv(t)
	e.push(map[string]string{
		".ard/config.yaml":            rolesCfg,
		".ard/triage/lead/prompt.txt": "local\n",
	})
	e.withShared(shared)
	res, err := e.source.Load(context.Background())
	if err != nil || len(res.Roles) != 1 {
		t.Fatalf("Load = %+v, %v", res, err)
	}
}

func TestSharedSymlinkRejected(t *testing.T) {
	shared, e := newEnv(t), newEnv(t)
	shared.push(map[string]string{".ard/triage/lead/prompt.txt": "x\n"})
	if err := os.Symlink("prompt.txt", filepath.Join(shared.other, ".ard/triage/lead/link")); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, shared.other, "add", "-A")
	gittest.Run(t, shared.other, "commit", "-m", "link")
	gittest.Run(t, shared.other, "push", "origin", "main")
	e.push(map[string]string{".ard/config.yaml": rolesCfg})
	e.withShared(shared)
	if _, err := e.source.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Load error = %v, want symlink rejection", err)
	}
}

func TestSharedRoleOverride(t *testing.T) {
	shared, e := newEnv(t), newEnv(t)
	shared.push(map[string]string{".ard/triage/lead/prompt.txt": "x\n"})
	e.push(map[string]string{".ard/config.yaml": rolesCfg + "overrides:\n  triage/lead: {model: m1}\n"})
	e.withShared(shared)
	res, err := e.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := res.Role(config.KindTriage, "lead"); r.Model != "m1" {
		t.Errorf("override not applied: %+v", r)
	}
	e.push(map[string]string{".ard/config.yaml": rolesCfg + "overrides:\n  triage/ghost: {model: m1}\n"})
	if _, err := e.source.Load(context.Background()); err == nil {
		t.Error("override of a missing role: want an error")
	}
}

func TestNoRolesRepoUnchanged(t *testing.T) {
	e := newEnv(t)
	e.push(map[string]string{".ard/config.yaml": "max_pods: 2\n"})
	res, err := e.source.Load(context.Background())
	if err != nil || res.RolesRepo != nil || res.RolesSHA != "" || res.Repo != filepath.Join(e.cache, res.SHA) {
		t.Fatalf("Load = %+v, %v", res, err)
	}
}

// An omitted ref follows the shared repo's default branch, whatever its name.
func TestSharedDefaultBranchMaster(t *testing.T) {
	shared, e := newEnv(t), newEnv(t)
	shared.push(map[string]string{".ard/triage/lead/prompt.txt": "on master\n"})
	gittest.Run(t, shared.bare, "branch", "-m", "main", "master")
	gittest.Run(t, shared.bare, "symbolic-ref", "HEAD", "refs/heads/master")
	e.push(map[string]string{".ard/config.yaml": rolesCfg})
	e.withShared(shared)
	res, err := e.source.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lead, ok := res.Role(config.KindTriage, "lead")
	if !ok || readFile(t, filepath.Join(res.Repo, lead.PromptPath)) != "on master\n" {
		t.Error("shared role from the master default branch not loaded")
	}
}
