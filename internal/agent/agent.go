// Package agent turns a job description into an agent CLI invocation and
// parses what the CLI reported. Adapters are pure: they never spawn a process,
// open a file or call the network. The executor owns the subprocess.
//
// agent imports nothing from config, executor or state; callers map their own
// types into Spec and Command.
package agent

import (
	"fmt"
	"io"
	"math"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Stage words match the label stage of ard:<stage>:<role>.
const (
	StageScanner = "scanner"
	StageTriage  = "triage"
	StageWorker  = "worker"
	StageReview  = "review"
)

// Spec describes one agent run. Untrusted inputs are paths only; the adapter
// never reads their contents.
type Spec struct {
	Stage        string   // one of the Stage* constants; named in the user message; every stage gets the same default tool profile
	Role         string   // ^[a-z0-9][a-z0-9-]*$
	Issue        int      // >= 1; output file is .ard/<Issue>/<Role>.md
	PromptFile   string   // absolute runtime path of the role's prompt.txt from the role mount
	Untrusted    []string // slash paths relative to the job's cwd, max 64
	ReadDirs     []string // absolute extra dirs the agent may read
	ConfigDir    string   // absolute, per-job, empty, outside the worktree
	Model        string   // "" = CLI default
	MaxBudgetUSD float64  // > 0, finite
	MaxTurns     int      // 0 = omit the flag
	Tools        []string // nil = the adapter's default profile
}

// Command is what the executor should run. Argv[0] is the binary; run with
// Dir = worktree and stdin = /dev/null.
type Command struct {
	Argv    []string
	Env     map[string]string // fixed, non-secret values the CLI needs
	PassEnv []string          // names of secrets the caller supplies from its credential store
}

// Result is what a finished run reported.
type Result struct {
	OK                bool // ExitCode == 0 && result event present && !is_error && subtype == "success"
	ExitCode          int
	Subtype           string // raw result.subtype
	IsError           bool
	TerminalReason    string   // raw result.terminal_reason, "" if absent
	APIErrorStatus    int      // result.api_error_status, 0 if absent
	Turns             *int     // nil when not reported
	CostUSD           *float64 // nil when not reported; an estimate, not a bill
	DurationMS        int64
	SessionID         string
	Model             string
	CLIVersion        string
	PermissionDenials int
	// Summary is the agent-written result text truncated to 2000 runes.
	// Unredacted: log only, never post or prompt with it.
	Summary      string
	SkippedLines int // malformed or overlong lines ignored
}

// Transient reports whether a failed run looks worth one automatic retry.
func (r Result) Transient() bool {
	if r.OK {
		return false
	}
	switch r.APIErrorStatus {
	case 429, 500, 502, 503, 529:
		return true
	}
	return false
}

// Adapter builds and parses one agent CLI.
type Adapter interface {
	Name() string
	// Command is pure; errors wrap ErrInvalidSpec.
	Command(s Spec) (Command, error)
	// Parse is pure. With no result event it returns the partial Result and
	// ErrNoResult.
	Parse(stdout io.Reader, exitCode int) (Result, error)
}

// Registry maps adapter names to adapters.
type Registry struct{ m map[string]Adapter }

// NewRegistry rejects duplicate names.
func NewRegistry(as ...Adapter) (*Registry, error) {
	r := &Registry{m: map[string]Adapter{}}
	for _, a := range as {
		if _, dup := r.m[a.Name()]; dup {
			return nil, fmt.Errorf("agent %q registered twice", a.Name())
		}
		r.m[a.Name()] = a
	}
	return r, nil
}

// Get returns the named adapter.
func (r *Registry) Get(name string) (Adapter, error) {
	a, ok := r.m[name]
	if !ok {
		return nil, fmt.Errorf("agent %q: %w", name, ErrUnknownAgent)
	}
	return a, nil
}

var (
	roleRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	untrustedRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	modelRE     = regexp.MustCompile(`^[A-Za-z0-9._:\[\]/-]+$`)
	toolRE      = regexp.MustCompile(`^[A-Za-z]+(\([ -~]*\))?$`)
)

// MaxUntrusted caps Spec.Untrusted.
const MaxUntrusted = 64

func invalid(field, why string) error {
	return fmt.Errorf("spec %s: %s: %w", field, why, ErrInvalidSpec)
}

func absPath(field, p string) error {
	switch {
	case !filepath.IsAbs(p):
		return invalid(field, "not absolute")
	case filepath.Clean(p) != p:
		return invalid(field, "not clean")
	case strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f || r == ',' }):
		return invalid(field, "control character or comma")
	}
	return nil
}

// Validate checks the shared Spec rules; adapters call it first in Command.
func (s Spec) Validate() error {
	switch s.Stage {
	case StageScanner, StageTriage, StageWorker, StageReview:
	default:
		return invalid("Stage", fmt.Sprintf("%q", s.Stage))
	}
	if !roleRE.MatchString(s.Role) {
		return invalid("Role", fmt.Sprintf("%q", s.Role))
	}
	if s.Issue < 1 {
		return invalid("Issue", "must be >= 1")
	}
	if err := absPath("PromptFile", s.PromptFile); err != nil {
		return err
	}
	if err := absPath("ConfigDir", s.ConfigDir); err != nil {
		return err
	}
	for _, d := range s.ReadDirs {
		if err := absPath("ReadDirs", d); err != nil {
			return err
		}
	}
	if len(s.Untrusted) > MaxUntrusted {
		return invalid("Untrusted", "too many entries")
	}
	seen := map[string]bool{}
	for _, u := range s.Untrusted {
		switch {
		case !untrustedRE.MatchString(u), strings.HasPrefix(u, "/"), path.Clean(u) != u,
			u == ".." || strings.HasPrefix(u, "../"):
			return invalid("Untrusted", fmt.Sprintf("%q", u))
		case seen[u]:
			return invalid("Untrusted", fmt.Sprintf("duplicate %q", u))
		}
		seen[u] = true
	}
	if !(s.MaxBudgetUSD > 0) || math.IsInf(s.MaxBudgetUSD, 0) {
		return invalid("MaxBudgetUSD", "must be > 0 and finite")
	}
	if s.MaxTurns < 0 {
		return invalid("MaxTurns", "negative")
	}
	if s.Model != "" && !modelRE.MatchString(s.Model) {
		return invalid("Model", fmt.Sprintf("%q", s.Model))
	}
	for _, t := range s.Tools {
		name, _, _ := strings.Cut(t, "(")
		if !toolRE.MatchString(t) || strings.ContainsAny(name, ",=") || strings.Contains(t, ",") {
			return invalid("Tools", fmt.Sprintf("%q", t))
		}
	}
	return nil
}
