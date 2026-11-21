//go:build integration

package claude

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
)

// Runs the real CLI with no API key, which forces an auth failure with no
// spend, and checks that it accepts every flag.
func TestCLIAcceptsFlags(t *testing.T) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	cfg, work := t.TempDir(), t.TempDir()
	prompt := work + "/prompt.txt"
	if err := os.WriteFile(prompt, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Binary: bin}).Command(agent.Spec{Stage: agent.StageTriage, Role: "default", Issue: 1,
		PromptFile: prompt, ConfigDir: cfg, MaxBudgetUSD: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work}
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	res, err := New(Options{}).Parse(&out, cmd.ProcessState.ExitCode())
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !res.IsError || res.TerminalReason != "api_error" {
		t.Errorf("unexpected %+v", res)
	}
}
