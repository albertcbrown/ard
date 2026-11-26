package scanner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

// Readier is an Engine that can say, before any GitHub call or slot, that it
// cannot run a role.
type Readier interface{ Ready(role config.Role) error }

// Engines routes a scan by role: a scan executable to Exec, a prompt.txt to
// Agent. A nil Agent makes every LLM scanner ErrNoCredential.
//
// Leave Agent unset rather than assigning a nil *AgentEngine: a typed nil in
// the interface is not nil, and its Scan would panic.
type Engines struct{ Exec, Agent Engine }

var (
	_ Engine  = Engines{}
	_ Readier = Engines{}
)

// Scan runs role on the engine Ready picks.
func (e Engines) Scan(ctx context.Context, cfg *config.Resolved, role config.Role, since time.Time) (ScanResult, error) {
	if err := e.Ready(role); err != nil {
		return ScanResult{}, err
	}
	if role.LLMScanner() {
		return e.Agent.Scan(ctx, cfg, role, since)
	}
	return e.Exec.Scan(ctx, cfg, role, since)
}

// Ready is nil for a deterministic scanner with an Exec engine. For an LLM
// scanner it is ErrNoCredential when Agent is nil, else Agent's Ready when
// Agent is a Readier.
func (e Engines) Ready(role config.Role) error {
	if !role.LLMScanner() {
		if e.Exec == nil {
			return errors.New("scanner: no engine for scan executables")
		}
		return nil
	}
	if e.Agent == nil {
		return fmt.Errorf("scanner %s: %w", role.Name, ErrNoCredential)
	}
	if r, ok := e.Agent.(Readier); ok {
		return r.Ready(role)
	}
	return nil
}
