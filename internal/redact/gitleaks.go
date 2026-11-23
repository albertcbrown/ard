package redact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// DefaultGitleaksTimeout bounds one gitleaks run.
const DefaultGitleaksTimeout = 2 * time.Minute

// gitleaksLeakExit is the exit code requested for "leaks found", so it is
// told apart from gitleaks' own error exit (1).
const gitleaksLeakExit = 2

// GitleaksOptions configure the gitleaks Scanner.
type GitleaksOptions struct {
	Path    string        // binary: absolute path or a name on PATH; default "gitleaks"
	Timeout time.Duration // default DefaultGitleaksTimeout
}

type gitleaks struct{ o GitleaksOptions }

// Gitleaks returns a Scanner that runs a gitleaks binary over the scanned
// bytes (detect --no-git). It fails closed: a missing binary, an error exit,
// a timeout or an unreadable report is an error. Findings carry only the
// rule, line and column, never the matched text.
// gitleaksWaitDelay is how long Scan waits for the output pipes after the scanner was killed (exec.Cmd.WaitDelay).
const gitleaksWaitDelay = time.Second

func Gitleaks(o GitleaksOptions) Scanner {
	if o.Path == "" {
		o.Path = "gitleaks"
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultGitleaksTimeout
	}
	return gitleaks{o}
}

type gitleaksReport struct {
	RuleID      string `json:"RuleID"`
	StartLine   int    `json:"StartLine"`
	StartColumn int    `json:"StartColumn"`
}

func (g gitleaks) Scan(ctx context.Context, r io.Reader) ([]Finding, error) {
	bin, err := exec.LookPath(g.o.Path)
	if err != nil {
		return nil, fmt.Errorf("gitleaks: binary not found: %w", err)
	}
	dir, err := os.MkdirTemp("", "ard-gitleaks-*")
	if err != nil {
		return nil, fmt.Errorf("gitleaks: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("gitleaks: temp dir: %w", err)
	}
	src := filepath.Join(dir, "input.log")
	report := filepath.Join(dir, "report.json")
	f, err := os.OpenFile(src, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("gitleaks: write input: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return nil, fmt.Errorf("gitleaks: write input: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("gitleaks: write input: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, g.o.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "detect", "--no-git", "--no-banner", "--redact",
		"--exit-code", fmt.Sprint(gitleaksLeakExit), "--source", src,
		"--report-format", "json", "--report-path", report)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	// Output is discarded: it may echo scanned content.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	// Wait copies that output through pipes, so a child the scanner started that still holds them would keep Wait
	// blocked after the timeout killed the scanner itself (#198). WaitDelay bounds that wait.
	cmd.WaitDelay = gitleaksWaitDelay
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if ctx.Err() != nil {
			return nil, fmt.Errorf("gitleaks: %w", ctx.Err())
		}
		if !errors.As(err, &ee) {
			return nil, fmt.Errorf("gitleaks: run: %w", err)
		}
		code = ee.ExitCode()
	}
	if code != 0 && code != gitleaksLeakExit {
		return nil, fmt.Errorf("gitleaks: exit status %d", code)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		return nil, fmt.Errorf("gitleaks: read report: %w", err)
	}
	var recs []gitleaksReport
	if err := json.Unmarshal(bytes.TrimSpace(data), &recs); err != nil {
		return nil, errors.New("gitleaks: report is not valid JSON")
	}
	if code == 0 && len(recs) > 0 {
		return nil, errors.New("gitleaks: report has findings but exit status 0")
	}
	if code == gitleaksLeakExit && len(recs) == 0 {
		return nil, errors.New("gitleaks: leak exit status with an empty report")
	}
	var out []Finding
	for _, rec := range recs {
		out = append(out, Finding{Rule: "gitleaks:" + rec.RuleID, Line: rec.StartLine, Col: rec.StartColumn})
	}
	return out, nil
}
