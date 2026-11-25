//go:build integration && unix

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/jobpod"
)

// stubAgent runs in the ard image's sh. It writes hello.txt and the role file,
// then prints what an agent could leak: its environment, mounts, process list and
// the secret directories.
const stubAgent = `cd /work || exit 3
echo hello > hello.txt
mkdir -p .ard/7
printf -- '---\nrole: engineer\nstatus: done\nattempt: 1\n---\nstub\n' > .ard/7/worker-engineer.md
echo "== env"; env
echo "== mountinfo"; cat /proc/self/mountinfo
echo "== proc"; ls /proc
echo "== secrets"; ls -d /var/run/secrets /var/run/ard /var/run/secrets/kubernetes.io/serviceaccount 2>&1
echo "== done"
`

// fakeTokens mints random read tokens and tracks which are live.
type fakeTokens struct {
	mu      sync.Mutex
	live    map[string]bool
	minted  []string
	revoked []string
}

func (f *fakeTokens) MintReadToken(_ context.Context, _ []github.Repo) (github.ScopedToken, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return github.ScopedToken{}, err
	}
	v := hex.EncodeToString(b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		f.live = map[string]bool{}
	}
	f.live[v] = true
	f.minted = append(f.minted, v)
	return github.ScopedToken{Value: v, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakeTokens) RevokeToken(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, v)
	f.revoked = append(f.revoked, v)
	return nil
}

func (f *fakeTokens) valid(v string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return v != "" && f.live[v]
}

func needInner(t *testing.T) {
	t.Helper()
	if os.Getenv("ARD_E2E_INNER") != "1" {
		t.Skip("runs only in the harness pod (ARD_E2E_INNER=1)")
	}
}

// TestInnerHold keeps the harness pod, and with it the -home claim, alive for the
// ReadWriteOncePod scenario.
func TestInnerHold(t *testing.T) {
	needInner(t)
	time.Sleep(75 * time.Second)
}

// TestInnerHappyPath runs one stub-agent job through the Kubernetes executor: the
// pod fetches (pre) from the fixture remote with a read token, the agent runs with no
// token or key, the controller pulls the result, and Cleanup removes the pod, its
// Secret and the token.
func TestInnerHappyPath(t *testing.T) {
	needInner(t)
	ctx := context.Background()
	scratch, err := os.MkdirTemp("/var/tmp", "e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { workspace.RemoveAllWritable(scratch) })

	// Fixture repo: main holds the role directory.
	bare, clone := gittest.NewRemote(t)
	roleDir := ".ard/workers/engineer"
	if err := os.MkdirAll(filepath.Join(clone, roleDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, roleDir, "prompt.txt"), []byte("prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "role")
	gittest.Run(t, clone, "push", "origin", "HEAD:main")
	sha := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	srv := filepath.Join(scratch, "srv", filepath.Dir(fixtureRep))
	if err := os.MkdirAll(srv, 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, scratch, "clone", "--bare", bare, filepath.Join(srv, filepath.Base(fixtureRep)+".git"))

	toks := &fakeTokens{}
	gitBase := gitServer(t, filepath.Join(scratch, "srv"), toks.valid, nil)

	client, err := api.InCluster(api.RESTOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(workspace.Options{Root: filepath.Join(scratch, "ard"), Git: local.ExecGit(""), CloneDir: clone,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	podImage, agentImage := os.Getenv("ARD_K8S_POD_IMAGE"), os.Getenv("ARD_K8S_AGENT_IMAGE")
	ex, err := kube.New(kube.Options{Client: client, Tokens: toks, WS: ws, Namespace: os.Getenv("ARD_K8S_NAMESPACE"),
		Instance: os.Getenv("ARD_K8S_INSTANCE"), Repo: fixtureRep, AgentImage: agentImage, PodImage: podImage, GitBase: gitBase,
		PreBudget: 3 * time.Minute, ResultGrace: 2 * time.Minute, Poll: 2 * time.Second,
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err != nil {
		t.Fatal(err)
	}

	h, err := ex.Prepare(ctx, executor.Job{ID: "01E2EHAPPYPATH00000000000", Repo: fixtureRep, Issue: 7, Stage: "worker", Role: "engineer",
		RepoDir: clone, Ref: "HEAD", RoleDir: filepath.Join(clone, roleDir), Network: executor.NetworkAdvisory,
		RoleSource: executor.RoleSource{SHA: sha, Path: roleDir}, Scrub: []string{".ard/7/worker-engineer.md"},
		Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	cleanup := func() {
		if !cleaned {
			cleaned = true
			if err := ex.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	}
	t.Cleanup(cleanup)
	h, err = ex.Start(ctx, h, executor.Command{Path: "/bin/sh", Args: []string{"-c", stubAgent}})
	if err != nil {
		t.Fatal(err)
	}
	podName := jobpod.PodName(h.JobID)

	var st executor.Status
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		if st, err = ex.Status(ctx, h); err != nil {
			t.Fatal(err)
		}
		if st.State.Terminal() {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if st.State != executor.Exited || st.ExitCode != 0 {
		t.Fatalf("status = %+v, want Exited 0", st)
	}
	if b, err := os.ReadFile(filepath.Join(h.Worktree, "hello.txt")); err != nil || strings.TrimSpace(string(b)) != "hello" {
		t.Fatalf("pulled result lacks hello.txt: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(h.Worktree, ".ard/7/worker-engineer.md")); err != nil {
		t.Fatalf("pulled result lacks the role file: %v", err)
	}

	// AgentHasNoToken: the agent's stdout (teed) holds none of the secrets.
	rc, err := ex.Logs(ctx, h, executor.LogsOptions{Stream: executor.Stdout})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	out := string(b)
	if !strings.Contains(out, "== done") {
		t.Fatalf("the agent's output is missing or truncated:\n%s", out)
	}
	toks.mu.Lock()
	minted := append([]string(nil), toks.minted...)
	toks.mu.Unlock()
	if len(minted) != 1 {
		t.Fatalf("minted %d tokens, want 1", len(minted))
	}
	for _, secret := range []string{minted[0], b64(minted[0])} {
		if strings.Contains(out, secret) {
			t.Fatalf("the agent's output contains the read token (or its base64 form)")
		}
	}
	sec := out[strings.Index(out, "== secrets"):]
	for _, bad := range []string{"/var/run/ard", "/var/run/secrets"} {
		if strings.Contains(sec, bad+"\n") {
			t.Errorf("%s exists in the agent container:\n%s", bad, sec)
		}
	}
	if m := out[strings.Index(out, "== mountinfo"):strings.Index(out, "== proc")]; strings.Contains(m, "tmpfs") && strings.Contains(m, "secret") {
		t.Errorf("a secret-backed mount is visible to the agent:\n%s", m)
	}

	// The Pod spec: the agent container mounts no Secret volume and no token.
	pod, err := client.GetPod(ctx, os.Getenv("ARD_K8S_NAMESPACE"), podName)
	if err != nil {
		t.Fatal(err)
	}
	secretVols := map[string]bool{}
	for _, v := range pod.Spec.Volumes {
		if v.Secret != nil {
			secretVols[v.Name] = true
		}
	}
	for _, c := range pod.Spec.Containers {
		if c.Name != jobpod.ContainerAgent {
			continue
		}
		for _, m := range c.VolumeMounts {
			if secretVols[m.Name] {
				t.Errorf("agent mounts Secret volume %s", m.Name)
			}
		}
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("automountServiceAccountToken is not false")
	}

	// Cleanup removes the pod, the Secret and the token.
	cleanup()
	if _, err := client.GetPod(ctx, os.Getenv("ARD_K8S_NAMESPACE"), podName); err == nil {
		// Deletion has a grace period: wait for it.
		for i := 0; i < 40 && err == nil; i++ {
			time.Sleep(3 * time.Second)
			_, err = client.GetPod(ctx, os.Getenv("ARD_K8S_NAMESPACE"), podName)
		}
		if err == nil {
			t.Errorf("pod %s still exists after Cleanup", podName)
		}
	}
	if toks.valid(minted[0]) {
		t.Errorf("the read token is still live after Cleanup")
	}
	if err := exec.Command("git", "ls-remote", strings.Replace(gitBase, "http://", "http://x-access-token:"+minted[0]+"@", 1)+"/"+fixtureRep+".git").Run(); err == nil {
		t.Errorf("the git server still accepts the revoked token")
	}
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
