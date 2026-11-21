// Package labels is the one place in the module that spells an ard: label.
// It holds the static labels, the constructors for the grammar
// ard:<stage>:<role>[:<state>], Parse (their inverse) and Desired, the
// catalogue of labels a repository needs. Every other package builds label
// names through these functions; literal_test.go enforces it.
//
// The package imports only internal/config and the standard library so any
// package can use it without pulling in GitHub or the effect layer.
package labels

import "strings"

// Prefix starts every label the controller owns.
const Prefix = "ard:"

// Stage is the second element of a role label.
type Stage string

// The three stages that carry role labels. config.KindReviewer maps to
// StageReview (see StageOf).
const (
	StageTriage Stage = "triage"
	StageWorker Stage = "worker"
	StageReview Stage = "review"
)

// State is the optional last element of a role label.
type State string

// States. StateChanges is valid only for StageReview.
const (
	StateRequest State = ""
	StateRunning State = "running"
	StateDone    State = "done"
	StateChanges State = "changes"
)

// The labels outside the role grammar.
const (
	Pending        = "ard:pending"
	ExecutorFailed = "ard:executor:failed"
	NeedsHuman     = "ard:needs-human"
	NeedsApproval  = "ard:needs-approval"
	Hold           = "ard:hold"
	Approved       = "ard:approved"
	TestsFailed    = "ard:tests:failed"
	Keep           = "ard:keep"
	Pause          = "ard:pause"
)

// Static lists the nine static labels in a fixed order.
func Static() []string {
	return []string{Pending, ExecutorFailed, NeedsHuman, NeedsApproval, Hold, Approved, TestsFailed, Keep, Pause}
}

// Name builds ard:<stage>:<role>[:<state>]. Request(StageTriage, "lead") is
// the single special case and returns Pending. Names are not validated here:
// callers pass roles that config has already validated, and Desired
// validates the catalogue.
func Name(s Stage, role string, st State) string {
	if s == StageTriage && role == "lead" && st == StateRequest {
		return Pending
	}
	n := Prefix + string(s) + ":" + role
	if st != StateRequest {
		n += ":" + string(st)
	}
	return n
}

// Request is the label that asks a role to run.
func Request(s Stage, role string) string { return Name(s, role, StateRequest) }

// Running marks a claim.
func Running(s Stage, role string) string { return Name(s, role, StateRunning) }

// Done marks completion.
func Done(s Stage, role string) string { return Name(s, role, StateDone) }

// Changes is a reviewer's request-changes verdict.
func Changes(role string) string { return Name(StageReview, role, StateChanges) }

// Scanner is the label a scanner puts on the issues it files.
func Scanner(name string) string { return Prefix + "scanner:" + name }

// ParseStage parses a stage word as written in `next:` entries.
func ParseStage(s string) (Stage, bool) {
	switch Stage(s) {
	case StageTriage, StageWorker, StageReview:
		return Stage(s), true
	}
	return "", false
}

// Kind classifies a parsed label.
type Kind int

// Label kinds.
const (
	KindStatic Kind = iota + 1
	KindScanner
	KindRole
)

// Parsed is the result of Parse.
type Parsed struct {
	Kind  Kind
	Name  string // the input, lower-cased
	Stage Stage  // KindRole only
	Role  string // KindRole: role; KindScanner: scanner name
	State State  // KindRole only
}

// Parse is the inverse of the constructors. It matches case-insensitively,
// as GitHub does. It checks the static set first, so ard:executor:failed and
// ard:tests:failed never parse as stage "executor" or "tests". After that it
// accepts only the three known stages and the states valid for each stage
// (changes only for review). ard:pending parses as KindStatic, not as the
// lead's request. ard:triage:lead itself is not a valid label and returns
// false.
func Parse(name string) (Parsed, bool) {
	n := strings.ToLower(name)
	for _, s := range Static() {
		if n == s {
			return Parsed{Kind: KindStatic, Name: n}, true
		}
	}
	rest, ok := strings.CutPrefix(n, Prefix)
	if !ok {
		return Parsed{}, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Parsed{}, false
	}
	if parts[0] == "scanner" {
		if len(parts) != 2 || !ValidRoleName(parts[1]) {
			return Parsed{}, false
		}
		return Parsed{Kind: KindScanner, Name: n, Role: parts[1]}, true
	}
	stage, ok := ParseStage(parts[0])
	if !ok || !ValidRoleName(parts[1]) {
		return Parsed{}, false
	}
	st := StateRequest
	if len(parts) == 3 {
		switch State(parts[2]) {
		case StateRunning, StateDone:
			st = State(parts[2])
		case StateChanges:
			if stage != StageReview {
				return Parsed{}, false
			}
			st = StateChanges
		default:
			return Parsed{}, false
		}
	}
	if stage == StageTriage && parts[1] == "lead" && st == StateRequest {
		return Parsed{}, false // the lead's request is ard:pending
	}
	return Parsed{Kind: KindRole, Name: n, Stage: stage, Role: parts[1], State: st}, true
}

// ValidRoleName reports whether name matches ^[a-z0-9][a-z0-9-]*$, the same
// pattern as config's role name pattern.
func ValidRoleName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}
