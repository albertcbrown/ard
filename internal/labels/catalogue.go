package labels

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/config"
)

// GitHub's limits on a label.
const (
	maxNameLen = 50
	maxDescLen = 100
)

// Label is one label definition. Color is six lowercase hex digits without '#'.
type Label struct{ Name, Color, Description string }

// Spec names the roles and scanners a repository has.
type Spec struct{ Triage, Workers, Reviewers, Scanners []string }

// SpecFrom takes the role names out of a resolved configuration.
func SpecFrom(r *config.Resolved) Spec {
	names := func(k config.Kind) []string {
		var out []string
		for _, role := range r.RolesOf(k) {
			out = append(out, role.Name)
		}
		return out
	}
	return Spec{
		Triage:    names(config.KindTriage),
		Workers:   names(config.KindWorker),
		Reviewers: names(config.KindReviewer),
		Scanners:  names(config.KindScanner),
	}
}

// StageOf maps a role kind to its label stage. Scanners have no stage.
func StageOf(k config.Kind) (Stage, bool) {
	switch k {
	case config.KindTriage:
		return StageTriage, true
	case config.KindWorker:
		return StageWorker, true
	case config.KindReviewer:
		return StageReview, true
	}
	return "", false
}

var roleStates = map[Stage][]State{
	StageTriage: {StateRequest, StateRunning, StateDone},
	StageWorker: {StateRequest, StateRunning, StateDone},
	StageReview: {StateRequest, StateRunning, StateDone, StateChanges},
}

// Desired builds the full catalogue, sorted by name and deterministic. It is
// the only code that decides which roles get which states. Every problem is
// collected and returned with errors.Join.
func Desired(sp Spec) ([]Label, error) {
	var (
		out  []Label
		errs []error
	)
	for _, s := range Static() {
		out = append(out, staticLabel(s))
	}
	roles := func(stage Stage, kind string, names []string) {
		for _, r := range names {
			if !ValidRoleName(r) {
				errs = append(errs, fmt.Errorf("labels: %s %q: invalid name (must match ^[a-z0-9][a-z0-9-]*$)", kind, r))
				continue
			}
			for _, st := range roleStates[stage] {
				l := roleLabel(stage, r, st)
				if l.Name == Pending {
					continue // the lead's request is the static ard:pending
				}
				out = append(out, l)
			}
		}
	}
	roles(StageTriage, "triage role", sp.Triage)
	roles(StageWorker, "worker", sp.Workers)
	roles(StageReview, "reviewer", sp.Reviewers)
	for _, n := range sp.Scanners {
		if !ValidRoleName(n) {
			errs = append(errs, fmt.Errorf("labels: scanner %q: invalid name (must match ^[a-z0-9][a-z0-9-]*$)", n))
			continue
		}
		out = append(out, scannerLabel(n))
	}
	if err := validate(out); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// validate checks GitHub's length limits and that names are unique
// case-insensitively. Errors name the role that owns the label.
func validate(ls []Label) error {
	var errs []error
	seen := map[string]bool{}
	for _, l := range ls {
		who := ""
		if p, ok := Parse(l.Name); ok {
			switch p.Kind {
			case KindScanner:
				who = fmt.Sprintf("scanner %q: ", p.Role)
			case KindRole:
				kind := map[Stage]string{StageTriage: "triage role", StageWorker: "worker", StageReview: "reviewer"}[p.Stage]
				who = fmt.Sprintf("%s %q: ", kind, p.Role)
			}
		}
		if n := len(l.Name); n > maxNameLen {
			errs = append(errs, fmt.Errorf("labels: %slabel %q is %d chars (max %d)", who, l.Name, n, maxNameLen))
		}
		if n := len(l.Description); n > maxDescLen {
			errs = append(errs, fmt.Errorf("labels: %slabel %q: description is %d chars (max %d)", who, l.Name, n, maxDescLen))
		}
		k := strings.ToLower(l.Name)
		if seen[k] {
			errs = append(errs, fmt.Errorf("labels: duplicate label %q", l.Name))
		}
		seen[k] = true
	}
	return errors.Join(errs...)
}
