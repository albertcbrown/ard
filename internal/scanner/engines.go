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
	_ Engine  = Unavailable{}
	_ Readier = Unavailable{}
)

// ErrExecutorUnsupported means the executor cannot run an LLM scanner (the
// Kubernetes executor has no output dir yet, #454). Like ErrNoCredential, the
// run is not recorded: the scanner stays due.
var ErrExecutorUnsupported = errors.New("scanner: the executor cannot run LLM scanners")

// Unavailable is an Engine that runs nothing: Ready and Scan return Err. Serve
// sets it as Engines.Agent when the executor cannot run LLM scanners.
type Unavailable struct{ Err error }

// Ready returns u.Err.
func (u Unavailable) Ready(config.Role) error { return u.Err }

// Scan returns u.Err and runs nothing.
func (u Unavailable) Scan(context.Context, *config.Resolved, config.Role, time.Time) (ScanResult, error) {
	return ScanResult{}, u.Err
}

// skipReason names why an engine is not ready, for the scanner.skip log.
func skipReason(err error) string {
	switch {
	case errors.Is(err, ErrNoCredential):
		return "no_model_credential"
	case errors.Is(err, ErrExecutorUnsupported):
		return "executor_unsupported"
	}
	return "engine_unavailable"
}

// notReady is e's Ready verdict on role, or nil when e is not a Readier.
func notReady(e Engine, role config.Role) error {
	if r, ok := e.(Readier); ok {
		return r.Ready(role)
	}
	return nil
}

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
