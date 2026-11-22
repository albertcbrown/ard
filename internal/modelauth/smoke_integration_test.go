//go:build integration

package modelauth_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/modelauth"
)

// TestSmokeOAuth makes one tiny paid call on the owner's subscription. It runs
// only when ARD_SMOKE_OAUTH=1, so an ordinary integration run never spends:
//
//	env -u ANTHROPIC_API_KEY ARD_SMOKE_OAUTH=1 go test -tags integration -run Smoke ./internal/modelauth/
func TestSmokeOAuth(t *testing.T) {
	if os.Getenv("ARD_SMOKE_OAUTH") != "1" {
		t.Skip("set ARD_SMOKE_OAUTH=1 to spend one tiny subscription call")
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" || os.Getenv("ANTHROPIC_API_KEY") != "" {
		t.Skip("needs CLAUDE_CODE_OAUTH_TOKEN set and ANTHROPIC_API_KEY unset")
	}
	adapter := claude.New(claude.Options{})
	cred, err := modelauth.Resolve(modelauth.ModeOAuthToken, adapter.CredentialEnv, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	_, repo := gittest.NewRemote(t)
	root := t.TempDir()
	l, err := local.New(local.Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	roleDir := t.TempDir()
	prompt := filepath.Join(roleDir, "prompt.txt")
	if err := os.WriteFile(prompt, []byte("Reply with the single word ok. Use no tools.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h, err := l.Prepare(ctx, executor.Job{ID: "SMOKE1", Repo: "o/r", Issue: 1, Stage: agent.StageTriage, Role: "smoke",
		RepoDir: repo, Ref: "HEAD", RoleDir: roleDir, TrustedInput: true, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Cleanup(context.Background(), h, executor.CleanupOptions{}) })

	realCfg, _ := os.UserHomeDir()
	if cd := h.View.ConfigDir; !strings.HasPrefix(cd, root) || cd == filepath.Join(realCfg, ".claude") {
		t.Fatalf("config dir %q is not fresh under %s", cd, root)
	}
	if st, err := os.Stat(h.View.ConfigDir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("config dir: %v %v", st, err)
	}
	cmd, err := adapter.Command(agent.Spec{Stage: agent.StageTriage, Role: "smoke", Issue: 1, PromptFile: prompt,
		ConfigDir: h.View.ConfigDir, Model: "haiku", MaxBudgetUSD: 0.05, Auth: cred.Auth})
	if err != nil {
		t.Fatal(err)
	}
	var fixed []string
	for k, v := range cmd.Env {
		fixed = append(fixed, k+"="+v)
	}
	sort.Strings(fixed)
	h, err = l.Start(ctx, h, executor.Command{Path: cmd.Argv[0], Args: cmd.Argv[1:], Env: cred.Env(), Fixed: fixed})
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	st, err := executor.WaitFor(wctx, l, h, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Open(h.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	res, err := adapter.Parse(out, st.ExitCode)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("run failed: %+v", res)
	}
	t.Logf("ok: cli %s cost_usd %v", res.CLIVersion, res.CostUSD)
	if err := l.Cleanup(ctx, h, executor.CleanupOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{h.Home, h.View.ConfigDir} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains after Cleanup: %v", p, err)
		}
	}
}
