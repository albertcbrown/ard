package pool

import "time"

// Candidate is one job a loop would like to start.
type Candidate struct {
	Class Class
	// Role is the role directory name, e.g. "lead", "security-review", "cve".
	Role string
	// Number is the issue (triage, worker) or PR (review) number, and 0 for
	// scanner runs. Negative numbers are allowed (dry-run synthetic issues).
	Number int
	// Since is when the job became eligible; required. Triage and worker use
	// the issue's created_at, reviewers the PR's created_at, and a scanner run
	// the time it became due (last run plus interval).
	Since time.Time
	// Priority orders candidates within a class: 0 (ard:p0) first, 3 last.
	// The zero value is P0, so callers must set it; labels.Priority returns 2
	// for an unlabelled item, and scanner runs use 2.
	Priority int
	// Agent marks a job that runs a limited agent (agent: claude); it counts
	// toward the one-job rule (#63).
	Agent bool
	// Serial means the job must not start while another Agent job is held or
	// granted earlier in the same batch. It is set per tick by Options.Annotate
	// from the current usage reading.
	Serial bool
}

// Key returns KeyFor(c.Class, c.Role, c.Number); callers never build keys by hand.
func (c Candidate) Key() string { return KeyFor(c.Class, c.Role, c.Number) }

// invalid returns the first rule the candidate breaks, or "".
func (c Candidate) invalid() string {
	switch {
	case !c.Class.valid():
		return "unknown class " + c.Class.String()
	case c.Role == "":
		return "empty role"
	case c.Since.IsZero():
		return "zero Since"
	case c.Class == ClassScanner && c.Number != 0:
		return "scanner candidate must have number 0"
	case c.Class != ClassScanner && c.Number == 0:
		return c.Class.String() + " candidate must have a non-zero number"
	}
	return ""
}

// less is a total order on distinct keys: class, then Priority (lower first), then oldest Since, then
// lower Number, then Role. It does not depend on input order.
func less(a, b Candidate) bool {
	if a.Class != b.Class {
		return a.Class < b.Class
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if !a.Since.Equal(b.Since) {
		return a.Since.Before(b.Since)
	}
	if a.Number != b.Number {
		return a.Number < b.Number
	}
	return a.Role < b.Role
}
