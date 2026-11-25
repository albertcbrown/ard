// Package cli builds the ard command tree. Subcommand behaviour is
// injected through Handlers; a nil field runs as a "not implemented"
// stub until the issue that owns the command wires its handler in.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
)

// Globals holds the values of the global flags shared by every command.
type Globals struct {
	Repo       string // "owner/repo"; validated when set
	ConfigPath string // default .ard/config.yaml in the target repo (#11)
	DryRun     bool
	LogLevel   string // debug|info|warn|error, default info
}

// Env is passed to every command handler.
type Env struct {
	Globals
	Log            *slog.Logger
	Stdout, Stderr io.Writer
}

// Handlers holds one function per implemented command. A nil field makes
// that command exit non-zero with an ErrNotImplemented message.
type Handlers struct {
	Serve        func(ctx context.Context, e *Env, o ServeOpts) error       // #15
	ConfigCheck  func(ctx context.Context, e *Env, o ConfigCheckOpts) error // #11
	LabelsSync   func(ctx context.Context, e *Env) error                    // #22
	RebuildState func(ctx context.Context, e *Env, scanner string) error    // #23
	Init         func(ctx context.Context, e *Env, o InitOpts) error        // #27
	Setup        func(ctx context.Context, e *Env, o SetupOpts) error       // #346
	JobPre       func(ctx context.Context, e *Env, o JobPreOpts) error      // #316
	JobExec      func(ctx context.Context, e *Env, o JobExecOpts) error     // #316
	JobPost      func(ctx context.Context, e *Env, o JobPostOpts) error     // #316
}

// ServeOpts is the parsed flag set of `ard serve`.
type ServeOpts struct {
	// Loops names the controller loops to run; empty means every loop (#187).
	Loops []string
}

// ConfigCheckOpts is the parsed flag set of `ard config check`.
type ConfigCheckOpts struct {
	// Ref is a commit to validate instead of the working tree; "" means the
	// working tree.
	Ref string
}

// InitOpts is the parsed flag set of `ard init`.
type InitOpts struct {
	// Version is the ard build version, recorded in .ard/ard-init.lock.
	Version string
	// PR opens the ard-init PR instead of writing the local tree.
	PR bool
}

// SetupOpts is the parsed flag set of `ard setup`.
type SetupOpts struct {
	Check bool
}

// JobPreOpts is the parsed flag set of `ard job-pre`.
type JobPreOpts struct {
	Deadline time.Time
	// GitBase is the git host URL, default https://github.com.
	GitBase string
}

// JobExecOpts is the parsed flag set of `ard job-exec`.
type JobExecOpts struct {
	Deadline time.Time
	// Grace is the time between SIGTERM and SIGKILL.
	Grace time.Duration
	// Argv is the agent command, the arguments after "--".
	Argv []string
}

// JobPostOpts is the parsed flag set of `ard job-post`.
type JobPostOpts struct {
	Deadline time.Time
}

// ErrNotImplemented is returned (wrapped) by stub commands whose handler
// has not been wired yet.
var ErrNotImplemented = errors.New("not implemented yet")

// ExitError lets a handler choose a specific non-zero exit code. Run maps
// it to ExitError.Code; any other handler error exits 1.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// App carries everything NewRoot needs: the flag-bound globals, the
// injected handlers, the build version and the per-invocation Env built
// by the root PersistentPreRunE.
type App struct {
	Globals  Globals
	Handlers Handlers
	Version  string

	env *Env
}

// run executes h with the Env built in PersistentPreRunE, tagging the
// logger with the invoked command and normalising handler errors to
// exit code 1 unless they already carry an ExitError.
func (a *App) run(cmd *cobra.Command, h func(ctx context.Context, e *Env) error) error {
	e := a.env
	e.Log = e.Log.With("cmd", cmd.CommandPath())
	if err := h(cmd.Context(), e); err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return err
		}
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

// notImpl is the error a stub command returns when its handler is nil.
func notImpl(cmdPath, issue string) error {
	if issue == "" {
		return fmt.Errorf("%s: %w", cmdPath, ErrNotImplemented)
	}
	return fmt.Errorf("%s: %w (tracked in %s)", cmdPath, ErrNotImplemented, issue)
}
