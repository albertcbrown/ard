package servecmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	kfake "github.com/albertcbrown/ard/internal/executor/kube/api/fake"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/github"
	ghfake "github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/jobpod"
	"github.com/albertcbrown/ard/internal/state"
)

func k8sOpts() cli.K8sOpts {
	return cli.K8sOpts{Namespace: "jobs", Instance: "ard", AgentImage: "agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PodImage: "pod@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

func k8sWiring(t *testing.T, k cli.K8sOpts, auth github.Auth) (*wiring, *kfake.Fake) {
	t.Helper()
	w := newTestWiring(t)
	f := kfake.New()
	w.K8s, w.Auth = k, auth
	w.newKube = func() (api.Client, error) { return f, nil }
	return w, f
}

func TestNewExecutorMode(t *testing.T) {
	app := &github.AppAuth{}
	t.Run("local", func(t *testing.T) {
		w := newTestWiring(t)
		w.cfg.Store(&config.Resolved{Executor: "local"})
		ex, err := w.newExecutor()
		if _, ok := ex.(*local.Local); err != nil || !ok {
			t.Fatalf("got %T, %v", ex, err)
		}
	})
	t.Run("k8s", func(t *testing.T) {
		w, _ := k8sWiring(t, k8sOpts(), app)
		w.cfg.Store(&config.Resolved{Executor: "k8s"})
		ex, err := w.newExecutor()
		if _, ok := ex.(*kube.Executor); err != nil || !ok {
			t.Fatalf("got %T, %v", ex, err)
		}
	})
	t.Run("no config with cluster flags is unknown, then resolves", func(t *testing.T) {
		w, _ := k8sWiring(t, k8sOpts(), app)
		if _, err := w.newExecutor(); !errors.Is(err, errModeUnknown) {
			t.Fatalf("err = %v", err)
		}
		w.cfg.Store(&config.Resolved{Executor: "k8s"})
		if _, err := w.newExecutor(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no config without flags is local", func(t *testing.T) {
		w := newTestWiring(t)
		ex, err := w.newExecutor()
		if _, ok := ex.(*local.Local); err != nil || !ok {
			t.Fatalf("got %T, %v", ex, err)
		}
	})
	t.Run("missing namespace names the flag", func(t *testing.T) {
		k := k8sOpts()
		k.Namespace = ""
		w, _ := k8sWiring(t, k, app)
		w.cfg.Store(&config.Resolved{Executor: "k8s"})
		if _, err := w.newExecutor(); err == nil || !strings.Contains(err.Error(), "--k8s-namespace") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("PAT is refused", func(t *testing.T) {
		w, _ := k8sWiring(t, k8sOpts(), github.NewTokenAuth("x"))
		w.cfg.Store(&config.Resolved{Executor: "k8s"})
		if _, err := w.newExecutor(); !errors.Is(err, github.ErrCannotScope) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExecutorDrift(t *testing.T) {
	w := newTestWiring(t)
	w.cfg.Store(&config.Resolved{Executor: "local"})
	if _, err := w.newExecutor(); err != nil {
		t.Fatal(err)
	}
	if err := w.checkExecutor(&config.Resolved{Executor: "local"}); err != nil {
		t.Fatal(err)
	}
	err := w.checkExecutor(&config.Resolved{Executor: "k8s"})
	if err == nil || !strings.Contains(err.Error(), "executor changed from local to k8s; restart ard serve") {
		t.Fatalf("err = %v", err)
	}
}

func TestLateK8sConfigWithPATPauses(t *testing.T) {
	w, _ := k8sWiring(t, k8sOpts(), github.NewTokenAuth("x"))
	if err := w.checkExecutor(&config.Resolved{Executor: "k8s"}); !errors.Is(err, github.ErrCannotScope) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseResources(t *testing.T) {
	r, err := parseResources("cpu=2,memory=4Gi,memory-limit=8Gi,ephemeral-storage=10Gi")
	if err != nil || r.Agent.Requests["cpu"] != "2" || r.Agent.Requests["memory"] != "4Gi" ||
		r.Agent.Limits["memory"] != "8Gi" || r.Agent.Limits["ephemeral-storage"] != "10Gi" {
		t.Fatalf("%+v, %v", r.Agent, err)
	}
	for _, bad := range []string{"cpu", "gpu=1", "cpu="} {
		if _, err := parseResources(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func jobPod(name, repo, job string) *api.Pod {
	return &api.Pod{Meta: api.ObjectMeta{Name: name, Labels: map[string]string{
		jobpod.LabelPartOf: "ard", jobpod.LabelComponent: jobpod.ComponentJob,
		jobpod.LabelInstance: jobpod.LabelValue("ard"), jobpod.LabelRepo: jobpod.RepoLabel(repo), jobpod.LabelJob: job,
	}}}
}

func TestSweepOrphans(t *testing.T) {
	w, f := k8sWiring(t, k8sOpts(), &github.AppAuth{})
	w.cfg.Store(&config.Resolved{Executor: "k8s"})
	ex, err := w.newExecutor()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	j, err := w.Store.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*api.Pod{jobPod("p-run", "o/r", j.ID), jobPod("p-fix", "o/r", j.ID+"f1"),
		jobPod("p-gone", "o/r", "GONE"), jobPod("p-other", "x/y", "GONE")} {
		if _, err := f.CreatePod(ctx, "jobs", p); err != nil {
			t.Fatal(err)
		}
	}
	if err := sweepOrphans(ctx, ex, w.Store, w.Log); err != nil {
		t.Fatal(err)
	}
	left, _ := f.ListPods(ctx, "jobs", "")
	got := map[string]bool{}
	for _, p := range left {
		got[p.Meta.Name] = true
	}
	if !got["p-run"] || !got["p-fix"] || !got["p-other"] || got["p-gone"] {
		t.Errorf("pods left: %v", got)
	}
	var _ executor.Executor = ex
}

func TestEnvironIncludesTokens(t *testing.T) {
	w := newTestWiring(t)
	w.exec = secretExec{}
	found := false
	for _, kv := range w.environ() {
		if strings.HasPrefix(kv, "ARD_K8S_TOKEN_0=tok-value") {
			found = true
		}
	}
	if !found {
		t.Error("token missing from environ")
	}
}

type secretExec struct{ jobExecutor }

func (secretExec) Secrets() []string { return []string{"tok-value"} }

func TestK8sRefusesPAT(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Setenv("HOME", t.TempDir())
		cfg, _ := repoWithRemote(t, "executor: k8s\n")
		gh := ghfake.New(ghfake.WithIdentity(github.Identity{Login: "ard[bot]", Kind: github.AuthToken}))
		b := fakeBuilder(gh, dry)
		pat := func(ctx context.Context, e *cli.Env, r github.Repo, s string) (deps, error) {
			d, err := b(ctx, e, r, s)
			d.Auth = github.NewTokenAuth("x")
			return d, err
		}
		err := serveLoops(context.Background(), env(t, "o/r", cfg, dry), pat, nil, k8sOpts())
		var ee *cli.ExitError
		if !errors.As(err, &ee) || ee.Code != 1 || !errors.Is(err, github.ErrCannotScope) {
			t.Errorf("dry=%v: err = %v", dry, err)
		}
	}
}
