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
	"time"
)

// Stage words match the label stage of ard:<stage>:<role>.
const (
	StageScanner = "scanner"
	StageTriage  = "triage"
	StageWorker  = "worker"
	StageReview  = "review"
)

// RoleFilePath is the report file a role writes:
// .ard/<issue>/<stage>-<role>.md. stage is a label stage (triage, worker,
// review; config.Kind spells the last one "reviewer"); issue is the positive
// issue number. It does not validate.
func RoleFilePath(issue int, stage, role string) string {
	return fmt.Sprintf(".ard/%d/%s-%s.md", issue, stage, role)
}

// Auth names which model credential the job receives. Adapters map it to
// their CLI's env var; the value never reaches the adapter.
type Auth string

// The supported credentials.
const (
	AuthAPIKey     Auth = "api_key"
	AuthOAuthToken Auth = "oauth_token"
)

// Spec describes one agent run. Untrusted inputs are paths only; the adapter
// never reads their contents.
type Spec struct {
	Stage        string   // one of the Stage* constants; named in the user message; every stage gets the same default tool profile
	Role         string   // ^[a-z0-9][a-z0-9-]*$
	Issue        int      // >= 1 (0 for StageScanner); output file is RoleFilePath(Issue, Stage, Role)
	PromptFile   string   // absolute runtime path of the role's prompt.txt from the role mount
	Untrusted    []string // slash paths relative to the job's cwd, max 64
	ReadDirs     []string // absolute extra dirs the agent may read
	ConfigDir    string   // absolute, per-job, empty, outside the worktree
	OutputFile   string   // absolute; the file a scanner-stage run writes its findings to; required for StageScanner, forbidden otherwise
	Model        string   // "" = CLI default
	MaxBudgetUSD float64  // > 0, finite
	MaxTurns     int      // 0 = omit the flag
	Tools        []string // nil = the adapter's default profile
	Network      bool     // from the role's network setting; never from issue text
	Auth         Auth     // required; AuthAPIKey or AuthOAuthToken
	// WorkerRoles is set for the triage stage only; from the resolved config, never from issue text.
	WorkerRoles []WorkerNetwork
}

// WorkerNetwork is one worker role and its network setting.
type WorkerNetwork struct {
	Role    string
	Network bool
}

// Command is what the executor should run. Argv[0] is the binary; run with
// Dir = worktree and stdin = /dev/null.
type Command struct {
	Argv    []string
	Env     map[string]string // fixed, non-secret values the CLI needs
	PassEnv []string          // names of secrets the caller supplies from its credential store
}

// LimitScope is which subscription limit a run hit, in the CLI's own words.
type LimitScope string

// The scopes. Session and weekly limits cover the whole account; a model
// limit covers one model family.
const (
	ScopeUnknown LimitScope = ""        // a limit signal with no scope
	ScopeSession LimitScope = "session" // the rolling session (5-hour) window
	ScopeWeekly  LimitScope = "weekly"
	ScopeModel   LimitScope = "model" // one model family; LimitHit.Family says which
)

// LimitSignal is the strongest evidence of a limit the stream held.
type LimitSignal string

// The signals, strongest first.
const (
	SignalRateLimitEvent LimitSignal = "rate_limit_event" // rejected rate_limit_event that carries a reset, a known scope or credits_required
	SignalLimitMessage   LimitSignal = "limit_message"    // CLI error text "You've hit your ... limit" on a CLI-synthesised error
	SignalRateLimitError LimitSignal = "rate_limit_error" // api_retry or assistant error rate_limit/billing_error, a bare rejected event, or a final 429
)

// LimitHit says the run met a usage or rate limit. Consumers act on it only
// when !Result.OK: a run that saw a rejection and then succeeded keeps it.
type LimitHit struct {
	Scope    LimitScope
	Family   string    // "opus" | "sonnet" | "" (whole account or unknown)
	ResetsAt time.Time // UTC; zero when the stream gave no machine-readable reset
	Signal   LimitSignal
}

// Usage is token counts. Missing or null fields count as 0.
type Usage struct{ Input, Output, CacheWrite, CacheRead int64 }

// Add returns the field-wise sum.
func (u Usage) Add(o Usage) Usage {
	return Usage{u.Input + o.Input, u.Output + o.Output, u.CacheWrite + o.CacheWrite, u.CacheRead + o.CacheRead}
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
	// LimitHit is set when the stream held a usage or rate limit signal. nil
	// means none. It is also set with ErrNoResult: a job killed while it waited
	// on a limit has api_retry lines and no result.
	LimitHit *LimitHit
	// ModelUsage is result.modelUsage by model id: the whole run, including
	// subagents and compaction. nil when absent.
	ModelUsage map[string]Usage
	// Usage is the run's token total: the sum of ModelUsage when that is
	// present (the docs prefer it for accounting), otherwise result.usage,
	// which covers the main loop only. nil when the result had neither.
	Usage *Usage
	// Windows are the subscription usage readings the stream carried, one per
	// window type, the last reading winning, sorted by Type. It is set whenever
	// the stream held readings, also with ErrNoResult. Readings are not
	// trusted text: only CLI-emitted events feed them.
	Windows []Window
}

// Window is one subscription usage-window reading (for example the 5-hour or
// the weekly window).
type Window struct {
	Type        string    // raw window name, e.g. "five_hour", "seven_day"
	Utilization float64   // exactly as the stream reports it (a 0-1 fraction or a percent); not normalised
	Status      string    // raw status, e.g. "allowed", "allowed_warning", "rejected"
	ResetsAt    time.Time // UTC; zero when absent
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
	if s.Stage == StageScanner {
		// A scanner reads the whole repository, all of it untrusted, and
		// writes only its output file; it has no issue.
		switch {
		case s.Issue != 0:
			return invalid("Issue", "must be 0 for a scanner")
		case len(s.Untrusted) > 0:
			return invalid("Untrusted", "a scanner lists none: the whole repository is untrusted")
		}
		if err := absPath("OutputFile", s.OutputFile); err != nil {
			return err
		}
	} else {
		switch {
		case s.Issue < 1:
			return invalid("Issue", "must be >= 1")
		case s.OutputFile != "":
			return invalid("OutputFile", "only a scanner has one")
		}
	}
	if s.Auth != AuthAPIKey && s.Auth != AuthOAuthToken {
		return invalid("Auth", fmt.Sprintf("%q", s.Auth))
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
