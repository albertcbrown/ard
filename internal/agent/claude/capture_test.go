//go:build integration && capture

// The capture harness records real stream-json transcripts for the fixtures
// in testdata/. It makes PAID model calls (except the auth and usage-limit
// cases, which are rejected), so it is behind its own build tag and runs only
// when ARD_CAPTURE names cases. testdata/CAPTURE.md has the commands.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/redact"
)

type captureCase struct {
	name     string // ARD_CAPTURE name
	fixture  string // real_<fixture>.jsonl; "" for usage_limit (named by its scope)
	prompt   string
	budget   float64
	maxTurns int
	cred     string // "token" | "bogus" | "none"
	timeout  time.Duration
}

const writeDone = "Write .ard/1/default.md with front matter `status: done` and one line, using only Write; do nothing else."

var captureCases = []captureCase{
	{name: "success", fixture: "success", prompt: writeDone, budget: 0.05, cred: "token", timeout: 5 * time.Minute},
	{name: "max_turns", fixture: "error_max_turns", prompt: "First read README.md. Then " + writeDone, budget: 0.05, maxTurns: 1, cred: "token", timeout: 5 * time.Minute},
	{name: "budget", fixture: "budget_exceeded", prompt: writeDone, budget: 0.0001, cred: "token", timeout: 5 * time.Minute},
	// A 401 makes the CLI retry for minutes: record how long and how many retries.
	{name: "auth_bad_token", fixture: "auth_bad_token", prompt: writeDone, budget: 0.05, cred: "bogus", timeout: 15 * time.Minute},
	{name: "auth_none", fixture: "auth_none", prompt: writeDone, budget: 0.05, cred: "none", timeout: 15 * time.Minute},
	// Opportunistic: only while the account is already at a limit (CAPTURE.md).
	{name: "usage_limit", prompt: writeDone, budget: 0.05, cred: "token", timeout: 30 * time.Minute},
}

// captureEnvFile holds the paths of a capture run, for the scrub step.
type captureEnvFile struct {
	Work, Home, ConfigDir, TmpDir, PromptPath string
}

func captureOut(t *testing.T) string {
	t.Helper()
	out := os.Getenv("ARD_CAPTURE_OUT")
	if !filepath.IsAbs(out) {
		t.Fatalf("ARD_CAPTURE_OUT must be an absolute directory outside the repo, got %q", out)
	}
	if fi, err := os.Stat(out); err != nil || !fi.IsDir() {
		t.Fatalf("ARD_CAPTURE_OUT %q is not a directory", out)
	}
	wd, _ := os.Getwd()
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if rel, err := filepath.Rel(d, out); err == nil && !strings.HasPrefix(rel, "..") {
				t.Fatalf("ARD_CAPTURE_OUT %q is inside the repo %q: raw captures must never be committed", out, d)
			}
			break
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return out
}

func TestCapture(t *testing.T) {
	sel := os.Getenv("ARD_CAPTURE")
	if sel == "" {
		t.Skip("ARD_CAPTURE not set")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal("claude is not on PATH")
	}
	ver, err := exec.Command(bin, "--version").Output()
	if err != nil || !strings.Contains(string(ver), TestedVersion) {
		t.Fatalf("claude --version = %q (%v), want %s: a capture from another release does not match the adapter", ver, err, TestedVersion)
	}
	if _, set := os.LookupEnv("ANTHROPIC_API_KEY"); set {
		t.Fatal("ANTHROPIC_API_KEY is set: unset it, the CLI would bill the API key instead of the subscription")
	}
	out := captureOut(t)
	token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	for _, name := range strings.Split(sel, ",") {
		var cc *captureCase
		for i := range captureCases {
			if captureCases[i].name == strings.TrimSpace(name) {
				cc = &captureCases[i]
			}
		}
		if cc == nil {
			t.Fatalf("unknown capture case %q", name)
		}
		if cc.cred == "token" && token == "" {
			t.Fatalf("case %s needs CLAUDE_CODE_OAUTH_TOKEN (from `claude setup-token`)", cc.name)
		}
		t.Run(cc.name, func(t *testing.T) { runCapture(t, bin, out, *cc, token) })
	}
}

func runCapture(t *testing.T, bin, out string, cc captureCase, token string) {
	// A repo holding only fixed dummy text: file contents in the transcript are
	// harmless by construction.
	work := filepath.Join(t.TempDir(), "work")
	writeFile(t, filepath.Join(work, "README.md"), "capture fixture\n")
	writeFile(t, filepath.Join(work, ".ard/1/issue.md"), "dummy\n")
	gittest.Run(t, work, "init", "--initial-branch=main")
	gittest.Run(t, work, "add", "-A")
	gittest.Run(t, work, "commit", "-m", "capture fixture")
	prompt := filepath.Join(t.TempDir(), "prompt.txt")
	writeFile(t, prompt, cc.prompt+"\n")
	cfg, home := t.TempDir(), t.TempDir()
	model := "haiku"
	if m := os.Getenv("ARD_CAPTURE_MODEL"); m != "" && cc.name == "usage_limit" {
		model = m
	}
	// The adapter's own Command, never a hand-typed command line.
	c, err := New(Options{Binary: bin}).Command(agent.Spec{Stage: agent.StageWorker, Role: "default", Issue: 1,
		PromptFile: prompt, ConfigDir: cfg, Model: model, MaxBudgetUSD: cc.budget, MaxTurns: cc.maxTurns, Auth: agent.AuthOAuthToken})
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home}
	switch cc.cred {
	case "token":
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
	case "bogus":
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+bogusToken)
	}
	start := time.Now()
	raw, code := runCLITimeout(t, cc.timeout, work, c, c.Argv, env...)
	secs := int(time.Since(start).Seconds())

	res, perr := New(Options{}).Parse(bytes.NewReader(raw), code)
	if perr != nil && !errors.Is(perr, agent.ErrNoResult) {
		t.Fatalf("Parse: %v", perr)
	}
	meta := realMeta{Case: cc.name, ExitCode: code, Seconds: secs, CLIVersion: res.CLIVersion,
		APIRetries: countEvents(raw, "system", "api_retry"), RateLimitEvents: countEvents(raw, "rate_limit_event", ""), ResultSubtype: res.Subtype}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(out, cc.name+".raw.jsonl"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, cc.name+".meta.json"), append(mb, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	eb, _ := json.Marshal(captureEnvFile{Work: work, Home: home, ConfigDir: cfg, TmpDir: os.TempDir(), PromptPath: prompt})
	if err := os.WriteFile(filepath.Join(out, cc.name+".env.json"), eb, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: exit %d in %ds, subtype %q, ok %v, api_retries %d, rate_limit_events %d, limit %+v, parse err %v",
		cc.name, code, secs, res.Subtype, res.OK, meta.APIRetries, meta.RateLimitEvents, res.LimitHit, perr)
}

// TestScrubCaptures turns <ARD_CAPTURE_OUT>/*.raw.jsonl into testdata/real_*.
// It fails closed: a transcript the scrubber or a second secret scan flags is
// deleted from testdata and fails the test.
func TestScrubCaptures(t *testing.T) {
	out := os.Getenv("ARD_CAPTURE_OUT")
	if out == "" {
		t.Skip("ARD_CAPTURE_OUT not set")
	}
	out = captureOut(t)
	raws, _ := filepath.Glob(filepath.Join(out, "*.raw.jsonl"))
	if len(raws) == 0 {
		t.Fatalf("no *.raw.jsonl in %s: run TestCapture first", out)
	}
	secrets := redact.FromEnviron(os.Environ())
	token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	if token != "" {
		cred, err := modelauth.Resolve(modelauth.ModeOAuthToken, New(Options{}).CredentialEnv, os.Getenv)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if err := cred.AddTo(secrets); err != nil {
			t.Fatal(err)
		}
	}
	red, err := redact.New(secrets, redact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, rawPath := range raws {
		name := strings.TrimSuffix(filepath.Base(rawPath), ".raw.jsonl")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(rawPath)
			if err != nil {
				t.Fatal(err)
			}
			var ef captureEnvFile
			if b, err := os.ReadFile(filepath.Join(out, name+".env.json")); err != nil || json.Unmarshal(b, &ef) != nil {
				t.Fatalf("%s.env.json: %v", name, err)
			}
			var meta realMeta
			if b, err := os.ReadFile(filepath.Join(out, name+".meta.json")); err != nil || json.Unmarshal(b, &meta) != nil {
				t.Fatalf("%s.meta.json: %v", name, err)
			}
			env := currentScrubEnv(ef.Work, ef.Home, ef.ConfigDir, ef.PromptPath, token)
			env.TmpDir = ef.TmpDir
			clean, err := scrubTranscript(raw, env)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := fixtureName(name, clean, meta.ExitCode)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join("testdata", "real_"+fixture+".jsonl")
			dstMeta := strings.TrimSuffix(dst, ".jsonl") + ".meta.json"
			// A second, independent scan with every credential-looking env value.
			res, rerr := red.Text(context.Background(), clean)
			if rerr != nil || res.Flagged {
				t.Fatalf("secret scan flagged the scrubbed transcript (error %v, findings %d): nothing written", rerr, len(res.Findings))
			}
			if !bytes.Equal(res.Clean, clean) {
				t.Fatalf("the redactor would change the scrubbed transcript: nothing written")
			}
			meta.Case = fixture
			mb, _ := json.MarshalIndent(meta, "", "  ")
			for path, data := range map[string][]byte{dst: clean, dstMeta: append(mb, '\n')} {
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("wrote %s and %s", dst, dstMeta)
		})
	}
}

// fixtureName maps a capture case to its fixture name. A usage-limit capture
// is named by the scope it hit, and must have hit one.
func fixtureName(name string, clean []byte, exit int) (string, error) {
	for _, cc := range captureCases {
		if cc.name == name && cc.fixture != "" {
			return cc.fixture, nil
		}
	}
	if name != "usage_limit" {
		return "", fmt.Errorf("unknown capture %q", name)
	}
	res, err := New(Options{}).Parse(bytes.NewReader(clean), exit)
	if err != nil && !errors.Is(err, agent.ErrNoResult) {
		return "", err
	}
	if res.OK || res.LimitHit == nil {
		return "", fmt.Errorf("the usage_limit capture shows no limit hit (ok %v): was the account actually limited?", res.OK)
	}
	scope := string(res.LimitHit.Scope)
	switch res.LimitHit.Scope {
	case agent.ScopeModel:
		scope = res.LimitHit.Family
	case agent.ScopeUnknown:
		scope = "unknown"
	}
	return "usage_limit_" + scope, nil
}
