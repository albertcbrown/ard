// Package config loads .ard/config.yaml and discovers the role directories
// under .ard/{scanners,triage,workers,reviewers}/ into one immutable
// Resolved value. Every other package consumes only Resolved; none parses
// YAML or walks the role directories itself. The package is pure: fs.FS in,
// values out.
package config

import (
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// Kind is the type of a role directory.
type Kind string

const (
	KindScanner  Kind = "scanner"
	KindTriage   Kind = "triage"
	KindWorker   Kind = "worker"
	KindReviewer Kind = "reviewer"
)

// kindDir maps each kind to its directory under .ard/.
var kindDir = map[Kind]string{
	KindScanner:  "scanners",
	KindTriage:   "triage",
	KindWorker:   "workers",
	KindReviewer: "reviewers",
}

// kindOrder is the canonical kind order: discovery and Resolved.Roles sort
// by kind first, then by name.
var kindOrder = []Kind{KindScanner, KindTriage, KindWorker, KindReviewer}

// Intervals holds the poll interval of every loop. Durations under one
// minute are rejected; the Reaper alone runs faster and is not
// configurable.
type Intervals struct {
	Scanners  Duration // default 4h
	Triage    Duration // default 15m
	Workers   Duration // default 5m
	Reviewers Duration // default 5m
	Merger    Duration // default 5m
}

// Stale configures the built-in stale-issue closer (#25); the schema lives
// here so config files can carry it before that issue lands.
type Stale struct {
	InactiveFor Duration // default 30d
	SuppressFor Duration // default 90d
}

// Redact configures optional extra passes of the close-out secret scan.
type Redact struct {
	Gitleaks Gitleaks
}

// Gitleaks configures the optional, fail-closed gitleaks pass.
type Gitleaks struct {
	Enabled bool   // default false
	Path    string // default "gitleaks"; absolute path or a name on PATH

	// BlockAutocloseWhenMissing: default false. When gitleaks is enabled but
	// cannot run, also stop scanner autoclose for that run.
	BlockAutocloseWhenMissing bool
}

// Route says where a scanner's issues go.
type Route string

const (
	RoutePending    Route = "pending"
	RouteNone       Route = "none"
	RouteNeedsHuman Route = "needs-human"
)

// IntakeMode says which human-filed issues may enter triage (decided, #2).
type IntakeMode string

const (
	IntakeAll   IntakeMode = "all"   // every human-filed issue
	IntakeLabel IntakeMode = "label" // only issues an authorised actor labelled
)

// Intake is the intake setting. Label is "" unless config.yaml sets it; ""
// means the lead's request label (ard:pending), which this package cannot
// spell because package labels owns every ard: literal. Consumers resolve it
// with intake.Label; nothing else reads Label directly.
type Intake struct {
	Mode  IntakeMode // default IntakeLabel
	Label string     // label mode only; "" = the lead's request label
}

// Sidecar is a read-only companion repo mounted into a role's job.
type Sidecar struct {
	Repo string // "owner/name"
	Ref  string // branch, tag or full 40-hex SHA; default "main"
}

// When gates a reviewer: a nil When means the reviewer runs on every PR.
// See Matches for the semantics.
type When struct {
	Paths  []string // run only when the diff touches one of these paths
	Triage []string // run only when one of these triage roles ran
}

// Role is one discovered role directory plus its resolved settings.
type Role struct {
	Kind Kind
	Name string
	Dir  string // ".ard/<type>/<name>", repo-relative slash form

	PromptPath string   // Dir + "/prompt.txt"; "" for a deterministic scanner
	ScanPath   string   // Dir + "/scan"; "" for LLM roles
	Dockerfile string   // Dir + "/Dockerfile"; "" => default ard-agent image
	Extra      []string // other files, relative to Dir, sorted, mounted read-only

	Agent   string        // role > global
	Model   string        // optional
	Timeout time.Duration // default 30m
	Sidecar string        // key in Resolved.Sidecars; "" = none

	// Scanner only.
	Every     time.Duration // default Intervals.Scanners
	MaxOpen   int           // default 5; 0 is never valid
	GroupSize int           // default 10; 0 is never valid
	Autoclose bool          // default true iff ScanPath != ""
	Route     Route         // default pending for executable scanners, none for LLM scanners

	// Triage only: hold this role's request until these triage roles, if
	// requested, are done. Ordering is owned by #15.
	After []string
	// PlanSection is the plan.md "## " heading this role may write (#130): the
	// starter role's built-in section or a plan_section override; "" = none.
	PlanSection string

	// Worker only.
	Tests           string   // default "make test"
	MaxTestFixes    int      // default 2; 0 is never valid
	MaxReviewRounds int      // default 3
	Reviewers       []string // reviewer role names
	// Diff guard thresholds on the PR's changed lines: above DiffWarnLines the
	// PR proceeds with a "large change" note; above DiffMaxLines it is held
	// for a human. Both at least 1, DiffWarnLines <= DiffMaxLines.
	DiffWarnLines int // default 1000
	DiffMaxLines  int // default 2000

	// Reviewer only; nil = always.
	When *When

	// Budget caps (#8): zero means unset — the job cap falls back to
	// Resolved.MaxBudgetUSD and the daily cap to Resolved.BudgetUSDPerDay.
	MaxBudgetUSD    float64
	BudgetUSDPerDay float64
}

// ReadsUntrusted reports whether the role's job reads untrusted input (issue
// bodies, comments, PR text, diffs): every triage, worker and reviewer role,
// and an LLM scanner. Only a deterministic scanner (an executable `scan`)
// does not. A scanner without ScanPath counts as LLM, so the check fails closed.
func (r Role) ReadsUntrusted() bool { return r.Kind != KindScanner || r.LLMScanner() }

// LLMScanner reports whether the role is a scanner run by an agent from
// prompt.txt rather than by a scan executable.
func (r Role) LLMScanner() bool { return r.Kind == KindScanner && r.ScanPath == "" }

// Resolved is the fully merged configuration: built-in defaults, then the
// global config, then per-role overrides.
type Resolved struct {
	Repo string // repo root: a working tree, or the snapshot directory of SHA
	// SHA is the commit the config and role directories were read from; empty
	// when loaded from a working tree. Job builders (#29, #33, #35, #37) use
	// Ref = SHA and resolve Role.Dir, Role.Dockerfile and Role.PromptPath
	// against Repo. The loader's caller sets it right after loading; Resolved
	// is read-only after that.
	SHA string

	MaxPods         int     // job slots shared by every loop; default 3
	Executor        string  // "local" or "k8s"
	Agent           string  // default agent CLI
	RequireApproval bool    // default true
	BudgetUSDPerDay float64 // whole-repo daily cap; default 50
	MaxBudgetUSD    float64 // default per-job cap; default 5
	GlobalMaxOpen   int     // open scanner issues across all scanners; default 30
	Paused          bool

	Intervals      Intervals
	Intake         Intake
	ProtectedPaths []string // merge.protected_paths: "dir/" = directory prefix, else doublestar glob; see Protected
	Stale          Stale
	Redact         Redact
	Sidecars       map[string]Sidecar
	Roles          []Role // sorted by Kind then Name
}

// Role returns the role of the given kind and name.
func (r *Resolved) Role(k Kind, name string) (Role, bool) {
	for _, role := range r.Roles {
		if role.Kind == k && role.Name == name {
			return role, true
		}
	}
	return Role{}, false
}

// RoleBudgetUSDPerDay is the role's own daily cap in USD. set is false when
// the role has no override (an override of 0 or less counts as unset); then
// only the repo-wide Resolved.BudgetUSDPerDay applies to it.
func (r *Resolved) RoleBudgetUSDPerDay(role Role) (cap float64, set bool) {
	if role.BudgetUSDPerDay > 0 {
		return role.BudgetUSDPerDay, true
	}
	return 0, false
}

// HostUntrusted returns the roles that read untrusted input and have no
// Dockerfile, so the local executor runs them as a host subprocess under the
// controller's uid. Canonical order (kind, then name). It returns nothing
// when Executor is "k8s", where host mode does not exist.
func (r *Resolved) HostUntrusted() []Role {
	if r.Executor == "k8s" {
		return nil
	}
	var out []Role
	for _, role := range r.Roles {
		if role.ReadsUntrusted() && role.Dockerfile == "" {
			out = append(out, role)
		}
	}
	return out
}

// SidecarFor returns the sidecar the role mounts; ok is false when it has none.
// Loops set executor.Sidecar.Name from role.Sidecar so errors name the key.
func (r *Resolved) SidecarFor(role Role) (sc Sidecar, ok bool) {
	if role.Sidecar == "" {
		return Sidecar{}, false
	}
	sc, ok = r.Sidecars[role.Sidecar]
	return sc, ok
}

// RolesOf returns every role of the given kind, in canonical order.
func (r *Resolved) RolesOf(k Kind) []Role {
	var out []Role
	for _, role := range r.Roles {
		if role.Kind == k {
			out = append(out, role)
		}
	}
	return out
}

// sortRoles orders roles by kind (kindOrder) then name.
func sortRoles(roles []Role) {
	sort.Slice(roles, func(i, j int) bool {
		if roles[i].Kind != roles[j].Kind {
			return kindRank(roles[i].Kind) < kindRank(roles[j].Kind)
		}
		return roles[i].Name < roles[j].Name
	})
}

func kindRank(k Kind) int {
	for i, kk := range kindOrder {
		if kk == k {
			return i
		}
	}
	return len(kindOrder)
}

// Matches reports whether the reviewer runs: a nil When always does;
// otherwise it does when any changed path matches any Paths entry, or any
// Triage role has run (triageRan). Matching is case-sensitive and anchored at
// the repo root. An entry ending in "/" is a directory, matched on a segment
// boundary; any other entry is a doublestar glob where "*" stays within a
// segment and "**" crosses them ("**/go.mod" matches a root go.mod, while a
// slash-free glob such as "go.mod" matches only at the root). A nil changed
// means the changed paths are unknown: a rule with Paths then matches (fail
// open); an empty non-nil slice means nothing changed.
func (w *When) Matches(changed []string, triageRan func(role string) bool) bool {
	if w == nil {
		return true
	}
	if changed == nil && len(w.Paths) > 0 {
		return true
	}
	for _, e := range w.Paths {
		for _, p := range changed {
			if matchWhen(e, p) {
				return true
			}
		}
	}
	if triageRan != nil {
		for _, t := range w.Triage {
			if triageRan(t) {
				return true
			}
		}
	}
	return false
}

func matchWhen(entry, p string) bool {
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(p, entry) || p == strings.TrimSuffix(entry, "/")
	}
	m, err := doublestar.Match(entry, p)
	if err != nil {
		// A malformed pattern (Load rejects them, so only a hand-built When
		// reaches this) fails closed: it matches nothing.
		return false
	}
	return m
}
