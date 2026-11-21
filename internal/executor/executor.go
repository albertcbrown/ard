// Package executor defines the contract for running one agent job somewhere
// and reporting back. It holds plain data types only and imports nothing from
// config, state, agent or github, so other executors (Kubernetes, Phase 3)
// can implement the same five calls.
package executor

import (
	"context"
	"io"
	"time"
)

// Executor runs one agent job. The controller keeps the Handle (as JSON) and
// passes it back on every call; the executor keeps no durable state.
type Executor interface {
	// Prepare creates the workspace, writes inputs and opens log files. It
	// starts no process.
	Prepare(ctx context.Context, j Job) (Handle, error)
	// Start launches c without blocking. The ctx does not bound the process:
	// only the job deadline and Cleanup stop it.
	Start(ctx context.Context, h Handle, c Command) (Handle, error)
	Status(ctx context.Context, h Handle) (Status, error)
	Logs(ctx context.Context, h Handle, o LogsOptions) (io.ReadCloser, error)
	// Cleanup stops the process if needed and removes the workspace. It never
	// deletes logs and is idempotent.
	Cleanup(ctx context.Context, h Handle, o CleanupOptions) error
}

// Job describes one run.
type Job struct {
	ID         string        // ULID minted at claim; ^[0-9A-Za-z]{1,64}$
	Repo       string        // "owner/repo"
	Issue      int           // 0 = scanner run with no issue
	Stage      string        // "scanner" | "triage" | "worker" | "review"
	Role       string        // ^[a-z0-9][a-z0-9-]*$
	RepoDir    string        // abs path of the controller's clone, already fetched
	Ref        string        // commit-ish resolved in RepoDir; the worktree is detached at it
	RoleDir    string        // abs host path of the role directory
	Dockerfile string        // abs host path; "" = host subprocess
	SidecarDir string        // abs host path; "" = none
	ReadOnly   bool          // worktree read-only: Docker mounts :ro, host chmod -R a-w after Prepare
	Inputs     []File        // written into the worktree by Prepare
	Timeout    time.Duration // > 0
	// TrustedInput: the job reads no untrusted input (a deterministic
	// scanner); only valid with Stage "scanner". The zero value means it
	// does, so a builder that forgets the field gets the host-mode warning.
	TrustedInput bool
}

// File is a worktree-relative file written 0644 by Prepare.
type File struct {
	Path string
	Data []byte
}

// Paths are the locations as the job process sees them.
type Paths struct{ Work, Role, Sidecar string }

// Command is what to run. Paths must come from Handle.View.
type Command struct {
	Path string
	Args []string
	Env  []string // "NAME=value" secrets; only names the executor allows (AllowEnv)
	// Fixed holds non-secret "NAME=value" settings the CLI needs (for example
	// an agent adapter's config dir). Only names on the executor's AllowFixed
	// list are accepted; the executor's own names, GitHub credentials and
	// secret-shaped names are always refused.
	Fixed []string
	Stdin io.Reader
}

// Handle identifies a prepared or running job. It is JSON-serialisable and
// holds no env values or secrets.
type Handle struct {
	JobID, Kind         string // Kind: "host" | "docker"
	Issue               int
	Stage, Role         string
	Worktree, Home      string
	RepoDir             string
	BaseSHA             string
	LogPath, StdoutPath string
	View                Paths
	Image, Container    string
	PID                 int
	ProcStart           string
	PreparedAt          time.Time
	StartedAt, Deadline time.Time
	ReadOnly            bool
	Timeout             time.Duration
	RoleDir, SidecarDir string // host paths
	TrustedInput        bool   // copied from Job by Prepare
}

// State is the lifecycle state of a job.
type State string

const (
	Prepared State = "prepared"
	Running  State = "running"
	Exited   State = "exited"
	TimedOut State = "timed_out"
	// Orphaned: alive with a verified start time but not tracked by this
	// process (the controller restarted).
	Orphaned State = "orphaned"
	// Gone: not tracked and not alive; no exit code known.
	Gone State = "gone"
)

// Terminal reports whether the job will not change state again.
func (s State) Terminal() bool { return s == Exited || s == TimedOut || s == Gone }

// Status is a snapshot of a job.
type Status struct {
	State      State
	ExitCode   int    // -1 if killed by a signal
	Signal     string // e.g. "killed"; "" if exited normally
	Alive      bool
	LastOutput time.Time // mtime of the combined log
	StartedAt  time.Time
	FinishedAt time.Time
	Reason     string
}

// LogStream selects which log file Logs reads.
type LogStream int

const (
	Combined LogStream = iota
	Stdout
)

// LogsOptions selects a stream and an optional tail.
type LogsOptions struct {
	Stream    LogStream
	TailLines int // 0 = whole file
}

// CleanupOptions tunes Cleanup.
type CleanupOptions struct{ KeepWorktree bool }

// WaitFor polls Status until the state is terminal or ctx ends.
func WaitFor(ctx context.Context, ex Executor, h Handle, every time.Duration) (Status, error) {
	if every <= 0 {
		every = 100 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		st, err := ex.Status(ctx, h)
		if err != nil {
			return st, err
		}
		if st.State.Terminal() {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-t.C:
		}
	}
}
