package servecmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/redact"
)

func TestWarnHost(t *testing.T) {
	role := func(k config.Kind, name, dockerfile, scan string) config.Role {
		return config.Role{Kind: k, Name: name, Dockerfile: dockerfile, ScanPath: scan}
	}
	lead := role(config.KindTriage, "lead", "", "")
	tests := []struct {
		name  string
		calls [][]config.Role
		want  []string // roles warned about, in order
	}{
		{"triage without Dockerfile warns once across calls",
			[][]config.Role{{lead}, {lead}}, []string{"lead"}},
		{"role added later warns",
			[][]config.Role{{lead}, {lead, role(config.KindWorker, "dev", "", "")}}, []string{"lead", "dev"}},
		{"Dockerfile role never warns",
			[][]config.Role{{role(config.KindTriage, "lead", ".ard/triage/lead/Dockerfile", "")}}, nil},
		{"deterministic scanner never warns",
			[][]config.Role{{role(config.KindScanner, "cve", "", ".ard/scanners/cve/scan")}}, nil},
		{"LLM scanner warns",
			[][]config.Role{{role(config.KindScanner, "lint", "", "")}}, []string{"lint"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logs, errs bytes.Buffer
			c := &cfgLoader{log: slog.New(slog.NewTextHandler(&logs, nil)), stderr: &errs}
			for _, roles := range tc.calls {
				c.warnHost(&config.Resolved{Executor: "local", Roles: roles})
			}
			if n := strings.Count(logs.String(), "executor.host_untrusted"); n != len(tc.want) {
				t.Fatalf("%d log lines, want %d:\n%s", n, len(tc.want), logs.String())
			}
			if n := strings.Count(errs.String(), "WARNING:"); n != len(tc.want) {
				t.Fatalf("%d stderr lines, want %d:\n%s", n, len(tc.want), errs.String())
			}
			for _, r := range tc.want {
				if !strings.Contains(logs.String(), "role="+r) {
					t.Errorf("no warning for %s:\n%s", r, logs.String())
				}
			}
		})
	}
}

func TestWarnIntakeOncePerValue(t *testing.T) {
	var logs bytes.Buffer
	c := &cfgLoader{log: slog.New(slog.NewTextHandler(&logs, nil))}
	cfg := func(m config.IntakeMode, l string) *config.Resolved {
		return &config.Resolved{Intake: config.Intake{Mode: m, Label: l}}
	}
	for _, r := range []*config.Resolved{
		cfg(config.IntakeLabel, "ard:needs-human"), cfg(config.IntakeLabel, "ard:needs-human"),
		cfg(config.IntakeLabel, "ard:hold"), cfg(config.IntakeLabel, "ard:intake"),
		cfg(config.IntakeLabel, ""), cfg(config.IntakeAll, "ard:pause"),
	} {
		c.warnIntake(r)
	}
	if n := strings.Count(logs.String(), "config.intake_invalid"); n != 2 {
		t.Errorf("%d warnings, want 2 (one per distinct invalid value):\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("not at error level:\n%s", logs.String())
	}
}

type captureCommenter struct{ bodies []string }

func (c *captureCommenter) CreateComment(_ context.Context, _ int, body string) (github.Comment, error) {
	c.bodies = append(c.bodies, body)
	return github.Comment{}, nil
}

func TestReportReplacesHostPaths(t *testing.T) {
	home := t.TempDir()
	host := closeout.HostPaths{Home: home, ArdRoot: filepath.Join(home, ".ard")}
	secret := filepath.Join(host.ArdRoot, "config", "o", "r", "abc123")
	err := fmt.Errorf("config: snapshot: %w", &fs.PathError{Op: "rename", Path: secret, Err: errors.New("no such file")})
	paths, perr := newPathRedactor(host)
	if perr != nil {
		t.Fatal(perr)
	}
	for name, p := range map[string]*redact.Secrets{"rules": paths, "no rules": nil} {
		cm := &captureCommenter{}
		c := &cfgLoader{fx: cm, log: slog.New(slog.NewTextHandler(io.Discard, nil)), issue: func() int { return 5 }, paths: p}
		c.report(context.Background(), err)
		if len(cm.bodies) != 1 {
			t.Fatalf("%s: %d comments", name, len(cm.bodies))
		}
		if strings.Contains(cm.bodies[0], home) {
			t.Errorf("%s: comment holds the host path: %s", name, cm.bodies[0])
		}
		if p != nil && !strings.Contains(cm.bodies[0], "~/.ard/config/o/r/abc123") {
			t.Errorf("%s: placeholder missing: %s", name, cm.bodies[0])
		}
	}
}
