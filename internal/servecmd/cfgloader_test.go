package servecmd

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
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
