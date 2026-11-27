package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

type readyEngine struct {
	fakeEngine
	ready error
}

func (e *readyEngine) Ready(config.Role) error { return e.ready }

func llmRole(name string) config.Role {
	return config.Role{Kind: config.KindScanner, Name: name, Dir: ".ard/scanners/" + name, PromptPath: ".ard/scanners/" + name + "/prompt.txt"}
}

func TestEngines(t *testing.T) {
	notReady := errors.New("not ready")
	tests := []struct {
		name      string
		role      config.Role
		agent     *readyEngine // nil: Agent unset
		wantErr   error
		wantExec  int
		wantAgent int
	}{
		{name: "deterministic goes to Exec", role: scannerRole("cve"), agent: &readyEngine{}, wantExec: 1},
		{name: "deterministic with no Agent", role: scannerRole("cve"), wantExec: 1},
		{name: "LLM goes to Agent", role: llmRole("arch"), agent: &readyEngine{}, wantAgent: 1},
		{name: "LLM with no Agent", role: llmRole("arch"), wantErr: ErrNoCredential},
		{name: "LLM with an Agent that is not ready", role: llmRole("arch"), agent: &readyEngine{ready: notReady}, wantErr: notReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &fakeEngine{sha: "s"}
			e := Engines{Exec: ex}
			ag := tt.agent
			if ag != nil {
				e.Agent = ag // never a typed nil
			}
			rerr := e.Ready(tt.role)
			_, serr := e.Scan(context.Background(), &config.Resolved{SHA: "s"}, tt.role, time.Time{})
			for _, err := range []error{rerr, serr} {
				if (tt.wantErr == nil) != (err == nil) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
			}
			agentCalls := 0
			if ag != nil {
				agentCalls = ag.calls
			}
			if ex.calls != tt.wantExec || agentCalls != tt.wantAgent {
				t.Errorf("exec %d, agent %d calls", ex.calls, agentCalls)
			}
		})
	}
}
