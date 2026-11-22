// Package config loads .ard/config.yaml and discovers the role directories
// under .ard/{scanners,triage,workers,reviewers}/ into one immutable
// Resolved value. Every other package consumes only Resolved; none parses
// YAML or walks the role directories itself. The package is pure: fs.FS in,
// values out.
package config

import (
	"sort"
	"time"
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
// Semantics are owned by #15; config only validates and exposes the shape.
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

	// Triage only: hold this role's request until these triage roles, if
	// requested, are done. Ordering is owned by #15.
	After []string

	// Worker only.
	Tests           string   // default "make test"
	MaxTestFixes    int      // default 2; 0 is never valid
	MaxReviewRounds int      // default 3
	Reviewers       []string // reviewer role names

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
func (r Role) ReadsUntrusted() bool { return r.Kind != KindScanner || r.ScanPath == "" }

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
