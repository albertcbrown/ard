package triage

import (
	"fmt"
	"sort"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/modelauth"
)

// Command maps an agent invocation to an executor command. It is pure. The
// adapter must ask for exactly the credential the controller holds.
func Command(ac agent.Command, cred modelauth.Credential) (executor.Command, error) {
	if len(ac.Argv) == 0 {
		return executor.Command{}, fmt.Errorf("agent command has no argv: %w", agent.ErrInvalidSpec)
	}
	if len(ac.PassEnv) != 1 || cred.IsZero() || ac.PassEnv[0] != cred.Name {
		return executor.Command{}, fmt.Errorf("agent wants a credential other than the one set: %w", agent.ErrInvalidSpec)
	}
	fixed := make([]string, 0, len(ac.Env))
	for k, v := range ac.Env {
		fixed = append(fixed, k+"="+v)
	}
	sort.Strings(fixed)
	return executor.Command{Path: ac.Argv[0], Args: ac.Argv[1:], Fixed: fixed, Env: cred.Env()}, nil
}
