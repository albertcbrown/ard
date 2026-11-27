//go:build unix

package kube

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
)

const (
	bns     = "builds"
	builder = "moby/buildkit@sha256:5987568f92dafbfb679879de59c1cd8d26597f5f6635c314e166164e2615870c"
	imgRepo = "ghcr.io/o/ard"
	dg1     = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dg2     = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

type fakeTree struct {
	mu      sync.Mutex
	entries []TreeEntry
	archive []byte
	gotSHA  []string
	err     error
}

func (f *fakeTree) LsTree(_ context.Context, _, sha, _ string) ([]TreeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotSHA = append(f.gotSHA, sha)
	return append([]TreeEntry(nil), f.entries...), f.err
}

func (f *fakeTree) Archive(_ context.Context, _, sha, _ string, max int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotSHA = append(f.gotSHA, sha)
	if int64(len(f.archive)) > max {
		return nil, errTooBig
	}
	return f.archive, nil
}

func goodTree() *fakeTree {
	return &fakeTree{entries: []TreeEntry{
		{"100644", "aaa", "Dockerfile"}, {"100755", "bbb", "run.sh"}}, archive: []byte("TAR-FROM-SHA")}
}

func riJob(id string) executor.Job {
	j := testJob(id)
	j.Dockerfile = "/role/Dockerfile"
	j.RoleSource.Path = ".ard/reviewers/security-review"
	return j
}

type registry struct {
	status int // manifest HEAD status
	digest string
	token  int
	heads  atomic.Int32
}

func (r *registry) handler(w http.ResponseWriter, q *http.Request) {
	if strings.HasPrefix(q.URL.Path, "/token") {
		if r.token != 0 && r.token != 200 {
			w.WriteHeader(r.token)
			return
		}
		w.Write([]byte(`{"token":"anon"}`))
		return
	}
	r.heads.Add(1)
	if q.Header.Get("Authorization") != "Bearer anon" || q.Method != "HEAD" ||
		!strings.HasPrefix(q.URL.Path, "/v2/o/ard/reviewers-security-review/manifests/h-") {
		w.WriteHeader(418)
		return
	}
	if r.digest != "" {
		w.Header().Set("Docker-Content-Digest", r.digest)
	}
	w.WriteHeader(r.status)
}

func riEnv(t *testing.T, tree *fakeTree, reg *registry, mut func(*RoleImages)) *env {
	t.Helper()
	e := newEnv(t, func(o *Options) {
		ri := &RoleImages{Namespace: bns, BuilderImage: builder, BuilderUID: 1000, Repository: imgRepo,
			PushSecret: "push", Budget: 5 * time.Second}
		if mut != nil {
			mut(ri)
		}
		o.RoleImages, o.RoleTree = ri, tree
	})
	e.serve(reg.handler)
	return e
}

// build completes each builder pod that appears: fn gets the pod name.
func (e *env) builds(fn func(name string)) (stop func() int) {
	var n atomic.Int32
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		seen := map[string]bool{}
		for {
			select {
			case <-done:
				return
			case <-time.After(2 * time.Millisecond):
			}
			pods, _ := e.fake.ListPods(context.Background(), bns, "")
			for _, p := range pods {
				if !seen[p.Meta.Name] {
					seen[p.Meta.Name] = true
					n.Add(1)
					fn(p.Meta.Name)
				}
			}
		}
	}()
	return func() int { close(done); wg.Wait(); return int(n.Load()) }
}

func (e *env) finish(name string, phase string, code int32, msg string, logs ...string) {
	e.fake.SetContainer(bns, name, "buildkit", api.ContainerState{Terminated: &api.StateTerminated{ExitCode: code, Message: msg}})
	e.fake.SetLogs(bns, name, "buildkit", logs)
	e.fake.SetPhase(bns, name, phase)
}

func meta(d string) string { return `{"containerimage.digest":"` + d + `","image.name":"x"}` }

func (e *env) pods() int {
	p, _ := e.fake.ListPods(context.Background(), bns, "")
	return len(p)
}

func TestRoleTagStable(t *testing.T) {
	a := []TreeEntry{{"100644", "a", "Dockerfile"}, {"100755", "b", "x.sh"}}
	base := roleTag(a, "base")
	if got := roleTag([]TreeEntry{a[1], a[0]}, "base"); got != base {
		t.Error("order changes the tag")
	}
	for name, got := range map[string]string{
		"blob": roleTag([]TreeEntry{{"100644", "c", "Dockerfile"}, a[1]}, "base"),
		"mode": roleTag([]TreeEntry{a[0], {"100644", "b", "x.sh"}}, "base"),
		"path": roleTag([]TreeEntry{a[0], {"100755", "b", "y.sh"}}, "base"),
		"base": roleTag(a, "other"),
	} {
		if got == base {
			t.Errorf("%s change keeps the tag", name)
		}
	}
	if !strings.HasPrefix(base, "h-") || len(base) != 34 {
		t.Errorf("tag %q", base)
	}
}

func TestRoleImageCachedSkipsBuild(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 200, digest: dg1}, nil)
	h, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Image != imgRepo+"/reviewers-security-review@"+dg1 {
		t.Errorf("image %q", h.Image)
	}
	for _, c := range e.fake.Calls() {
		if c == "CreatePod" || c == "CreateSecret" {
			t.Errorf("unexpected %s", c)
		}
	}
}

func TestRoleImageBuildSuccess(t *testing.T) {
	tree := goodTree()
	e := riEnv(t, tree, &registry{status: 404}, nil)
	var pod *api.Pod
	var sec *api.Secret
	stop := e.builds(func(n string) {
		pod, _ = e.fake.GetPod(context.Background(), bns, n)
		for i := 0; i < 200 && sec == nil; i++ {
			sec, _ = e.fake.Secret(bns, n)
			time.Sleep(time.Millisecond)
		}
		e.finish(n, "Succeeded", 0, meta(dg2))
	})
	h, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if n := stop(); n != 1 {
		t.Errorf("%d builder pods", n)
	}
	if err != nil {
		t.Fatal(err)
	}
	if h.Image != imgRepo+"/reviewers-security-review@"+dg2 {
		t.Errorf("image %q", h.Image)
	}
	sp := pod.Spec
	c := sp.Containers[0]
	if len(sp.Containers) != 1 || c.Name != "buildkit" || c.Image != builder || sp.ServiceAccountName != "ard-build" ||
		*c.SecurityContext.RunAsUser != 1000 || len(sp.InitContainers) != 0 || *sp.ActiveDeadlineSeconds != 5 ||
		sp.AutomountServiceAccountToken == nil || *sp.AutomountServiceAccountToken {
		t.Errorf("pod shape %+v", sp)
	}
	if pod.Meta.Labels["app.kubernetes.io/component"] != "ard-build" || pod.Meta.Labels["app.kubernetes.io/instance"] != "ard-test" {
		t.Errorf("labels %v", pod.Meta.Labels)
	}
	for _, v := range sp.Volumes {
		if v.Secret != nil && v.Secret.SecretName != "push" && v.Secret.SecretName != pod.Meta.Name {
			t.Errorf("secret volume %q", v.Secret.SecretName)
		}
	}
	if !strings.Contains(c.Command[2], "name='"+imgRepo+"/reviewers-security-review:h-") ||
		!strings.Contains(c.Command[2], "AGENT_IMAGE='"+DefaultAgentBase+"'") {
		t.Errorf("command %q", c.Command[2])
	}
	if sec == nil || string(sec.Data["context.tar"]) != "TAR-FROM-SHA" || sec.Meta.OwnerReferences[0].Name != pod.Meta.Name {
		t.Errorf("secret %+v", sec)
	}
	if e.pods() != 0 {
		t.Error("builder pod left behind")
	}
	if _, ok := e.fake.Secret(bns, pod.Meta.Name); ok {
		t.Error("secret left behind")
	}
}

func TestRoleImageContextFromSHA(t *testing.T) {
	tree := goodTree()
	e := riEnv(t, tree, &registry{status: 200, digest: dg1}, nil)
	j := riJob("j1")
	j.RoleDir = t.TempDir() // a differing worktree copy is never read
	if _, err := e.ex.Prepare(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	for _, s := range tree.gotSHA {
		if s != sha40 {
			t.Errorf("read at %q", s)
		}
	}
}

func TestRoleImagePrivatePackageBuilds(t *testing.T) {
	for name, reg := range map[string]*registry{"token401": {token: 401}, "head401": {status: 401}} {
		t.Run(name, func(t *testing.T) {
			e := riEnv(t, goodTree(), reg, nil)
			stop := e.builds(func(n string) { e.finish(n, "Succeeded", 0, meta(dg1)) })
			_, err := e.ex.Prepare(context.Background(), riJob("j1"))
			if n := stop(); err != nil || n != 1 {
				t.Errorf("err %v builds %d", err, n)
			}
		})
	}
}

func TestRoleImageRegistryDown(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 500}, nil)
	_, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err == nil || !strings.Contains(err.Error(), "registry check") || !errors.Is(err, ErrRoleImageBuild) {
		t.Fatalf("err %v", err)
	}
	if e.pods() != 0 {
		t.Error("built anyway")
	}
}

func TestRoleImageBuildFailure(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 404}, nil)
	tok := "ghp_" + strings.Repeat("a1B2", 9)
	e.builds(func(n string) {
		e.finish(n, "Failed", 1, "", "step 1", "pushing with "+tok, "error: boom")
	})
	h, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err == nil || !errors.Is(err, ErrRoleImageBuild) || !strings.Contains(err.Error(), "exit 1") ||
		!strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), tok) {
		t.Error("token in error")
	}
	if h.JobID != "" || e.ex.get("j1") != nil {
		t.Error("job left behind")
	}
	if e.pods() != 0 {
		t.Error("builder pod left behind")
	}
}

func TestRoleImageBuildTimeout(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 404}, func(r *RoleImages) { r.Budget = 50 * time.Millisecond })
	_, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err %v", err)
	}
	if e.pods() != 0 {
		t.Error("builder pod left behind")
	}
}

func TestRoleImageAdmissionDenied(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 404}, nil)
	e.fake.Err["CreatePod"] = errors.New("denied by policy")
	_, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err == nil || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("err %v", err)
	}
}

func TestRoleImageBadDigest(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 404}, nil)
	e.builds(func(n string) { e.finish(n, "Succeeded", 0, meta("sha256:nothex")) })
	_, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err == nil || !strings.Contains(err.Error(), "no digest") {
		t.Fatalf("err %v", err)
	}
}

func TestRoleImageSingleFlight(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 404}, nil)
	stop := e.builds(func(n string) {
		time.Sleep(50 * time.Millisecond)
		e.finish(n, "Succeeded", 0, meta(dg1))
	})
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j := riJob(id)
			j.Issue = len(id) + int(id[0])
			if h, err := e.ex.Prepare(context.Background(), j); err != nil || !strings.HasSuffix(h.Image, dg1) {
				t.Errorf("%s: %v %q", id, err, h.Image)
			}
		}()
	}
	wg.Wait()
	if n := stop(); n != 1 {
		t.Errorf("%d builder pods", n)
	}
}

func TestRoleImageRejects(t *testing.T) {
	big := goodTree()
	big.archive = make([]byte, 1<<20)
	for name, tc := range map[string]struct {
		tree *fakeTree
		path string
		want string
	}{
		"symlink":    {&fakeTree{entries: []TreeEntry{{"100644", "a", "Dockerfile"}, {"120000", "b", "l"}}}, "", "symlink"},
		"submodule":  {&fakeTree{entries: []TreeEntry{{"100644", "a", "Dockerfile"}, {"160000", "b", "m"}}}, "", "symlink or submodule"},
		"noDockfile": {&fakeTree{entries: []TreeEntry{{"100644", "a", "run.sh"}}}, "", "no Dockerfile"},
		"oversize":   {big, "", "larger"},
		"badPath":    {goodTree(), ".ard/reviewers/../x", "role path"},
		"badType":    {goodTree(), ".ard/roles/dev", "role path"},
		"badName":    {goodTree(), ".ard/reviewers/Bad_Name", "role path"},
	} {
		t.Run(name, func(t *testing.T) {
			e := riEnv(t, tc.tree, &registry{status: 404}, nil)
			j := riJob("j1")
			if tc.path != "" {
				j.RoleSource.Path = tc.path
			}
			_, err := e.ex.Prepare(context.Background(), j)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v", err)
			}
			if e.pods() != 0 {
				t.Error("built")
			}
		})
	}
}

func TestPrepareDockerfileIgnoredWithoutRoleImages(t *testing.T) {
	e := newEnv(t, nil)
	h, err := e.ex.Prepare(context.Background(), riJob("j1"))
	if err != nil || h.Image != "agent@sha256:aa" {
		t.Fatalf("%v %q", err, h.Image)
	}
}

func TestAgentContainerUsesRoleImage(t *testing.T) {
	e := riEnv(t, goodTree(), &registry{status: 200, digest: dg1}, nil)
	ctx := context.Background()
	h, err := e.ex.Prepare(ctx, riJob("j1"))
	if err != nil {
		t.Fatal(err)
	}
	if h, err = e.ex.Start(ctx, h, testCmd()); err != nil {
		t.Fatal(err)
	}
	p, _ := e.fake.GetPod(ctx, ns, h.Container)
	if p.Spec.Containers[0].Image != h.Image || p.Spec.InitContainers[0].Image != "ard@sha256:bb" ||
		p.Spec.Containers[1].Image != "ard@sha256:bb" {
		t.Errorf("images %q %q %q", p.Spec.Containers[0].Image, p.Spec.InitContainers[0].Image, p.Spec.Containers[1].Image)
	}
}

func TestNewValidatesRoleImages(t *testing.T) {
	for name, mut := range map[string]func(*RoleImages){
		"no push secret":    func(r *RoleImages) { r.PushSecret = "" },
		"unpinned builder":  func(r *RoleImages) { r.BuilderImage = "moby/buildkit:rootless" },
		"repo without path": func(r *RoleImages) { r.Repository = "ghcr.io" },
	} {
		t.Run(name, func(t *testing.T) {
			ri := &RoleImages{Namespace: bns, BuilderImage: builder, BuilderUID: 1000, Repository: imgRepo, PushSecret: "p"}
			mut(ri)
			_, err := New(Options{Client: newEnv(t, nil).fake, Tokens: &tokens{}, WS: newEnv(t, nil).ws, Namespace: ns, Repo: "o/r",
				Instance: "i", AgentImage: "a@sha256:aa", PodImage: "p@sha256:bb", RoleImages: ri})
			if err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestExecTreeReadsPinnedCommit(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@e.x", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Skipf("git unavailable or failed: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	role := filepath.Join(dir, ".ard", "reviewers", "x")
	os.MkdirAll(role, 0o755)
	os.WriteFile(filepath.Join(role, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "one")
	sha := git("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(role, "Dockerfile"), []byte("FROM changed\n"), 0o644) // worktree differs
	tr := ExecTree("")
	es, err := tr.LsTree(context.Background(), dir, sha, ".ard/reviewers/x")
	if err != nil || len(es) != 1 || es[0].Path != "Dockerfile" || es[0].Mode != "100644" {
		t.Fatalf("%+v %v", es, err)
	}
	b, err := tr.Archive(context.Background(), dir, sha, ".ard/reviewers/x", 1<<20)
	if err != nil || !bytes.Contains(b, []byte("FROM scratch")) || bytes.Contains(b, []byte("changed")) {
		t.Fatalf("archive err %v", err)
	}
	if _, err := tr.Archive(context.Background(), dir, sha, ".ard/reviewers/x", 10); !errors.Is(err, errTooBig) {
		t.Errorf("err %v", err)
	}
}
