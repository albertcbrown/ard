package config

import (
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/albertcbrown/ard/internal/gitref"
	"github.com/albertcbrown/ard/internal/repopath"
)

// Built-in defaults, applied before the global config and per-role
// overrides. Anything unset in .ard/config.yaml resolves to these values.
const (
	defaultMaxPods         = 3 // exported as DefaultMaxPods
	defaultExecutor        = "local"
	defaultAgent           = "claude"
	defaultRequireApproval = true
	defaultBudgetUSDPerDay = 50.0
	defaultMaxBudgetUSD    = 5.0
	defaultGlobalMaxOpen   = 30
	defaultIntakeMode      = IntakeLabel

	defaultIntervalScanners  = Duration(4 * time.Hour)
	defaultIntervalTriage    = Duration(15 * time.Minute)
	defaultIntervalWorkers   = Duration(5 * time.Minute)
	defaultIntervalReviewers = Duration(5 * time.Minute)
	defaultIntervalMerger    = Duration(5 * time.Minute)

	defaultRoleTimeout = 30 * time.Minute

	defaultScannerMaxOpen   = 5
	defaultScannerGroupSize = 10

	defaultWorkerTests           = "make test"
	defaultWorkerMaxTestFixes    = 2
	defaultWorkerMaxReviewRounds = 3

	defaultGitleaksPath     = "gitleaks"
	defaultStaleInactiveFor = Duration(30 * 24 * time.Hour)
	defaultStaleSuppressFor = Duration(90 * 24 * time.Hour)

	defaultSidecarRef = "main"
)

// minLoopInterval is the fastest any configurable loop may poll; the
// Reaper alone runs every minute and is not configurable.
const minLoopInterval = Duration(time.Minute)

// newResolved returns a Resolved holding only the built-in defaults.
func newResolved(repoRoot string) *Resolved {
	return &Resolved{
		Repo:            repoRoot,
		MaxPods:         defaultMaxPods,
		Executor:        defaultExecutor,
		Agent:           defaultAgent,
		RequireApproval: defaultRequireApproval,
		BudgetUSDPerDay: defaultBudgetUSDPerDay,
		MaxBudgetUSD:    defaultMaxBudgetUSD,
		GlobalMaxOpen:   defaultGlobalMaxOpen,
		Intervals: Intervals{
			Scanners:  defaultIntervalScanners,
			Triage:    defaultIntervalTriage,
			Workers:   defaultIntervalWorkers,
			Reviewers: defaultIntervalReviewers,
			Merger:    defaultIntervalMerger,
		},
		Intake: Intake{Mode: defaultIntakeMode},
		Stale: Stale{
			InactiveFor: defaultStaleInactiveFor,
			SuppressFor: defaultStaleSuppressFor,
		},
		Redact:   Redact{Gitleaks: Gitleaks{Path: defaultGitleaksPath}},
		Sidecars: map[string]Sidecar{},
	}
}

// applyGlobals merges the raw global config into res, collecting errors.
// Called once per load, after parseConfigFile and before roles are built.
func applyGlobals(res *Resolved, raw rawConfig, cfgPath string) Errors {
	var errs Errors

	if raw.maxPods != nil {
		res.MaxPods = *raw.maxPods
	}
	if res.MaxPods < 1 {
		errs = append(errs, &Error{File: cfgPath, Key: "max_pods", Msg: fmt.Sprintf("must be at least 1, got %d", res.MaxPods)})
	}

	if raw.executor != nil {
		res.Executor = *raw.executor
	}
	if res.Executor != "local" && res.Executor != "k8s" {
		errs = append(errs, &Error{File: cfgPath, Key: "executor", Msg: fmt.Sprintf("must be %q or %q, got %q", "local", "k8s", res.Executor)})
	}

	if raw.agent != nil {
		res.Agent = *raw.agent
	}
	if strings.TrimSpace(res.Agent) == "" {
		errs = append(errs, &Error{File: cfgPath, Key: "agent", Msg: "must not be empty"})
	}

	if raw.requireApproval != nil {
		res.RequireApproval = *raw.requireApproval
	}
	if raw.paused != nil {
		res.Paused = *raw.paused
	}

	if raw.budgetUSDPerDay != nil {
		res.BudgetUSDPerDay = *raw.budgetUSDPerDay
	}
	if res.BudgetUSDPerDay <= 0 {
		errs = append(errs, &Error{File: cfgPath, Key: "budget_usd_per_day", Msg: fmt.Sprintf("must be greater than 0 (unlimited budgets are not allowed), got %g", res.BudgetUSDPerDay)})
	}

	if raw.maxBudgetUSD != nil {
		res.MaxBudgetUSD = *raw.maxBudgetUSD
	}
	if res.MaxBudgetUSD <= 0 {
		errs = append(errs, &Error{File: cfgPath, Key: "max_budget_usd", Msg: fmt.Sprintf("must be greater than 0, got %g", res.MaxBudgetUSD)})
	}

	if raw.globalMaxOpen != nil {
		res.GlobalMaxOpen = *raw.globalMaxOpen
	}
	if res.GlobalMaxOpen < 1 {
		errs = append(errs, &Error{File: cfgPath, Key: "global_max_open", Msg: fmt.Sprintf("must be at least 1, got %d", res.GlobalMaxOpen)})
	}

	res.Intervals.Scanners = intervalOr(raw.intervals.scanners, res.Intervals.Scanners)
	res.Intervals.Triage = intervalOr(raw.intervals.triage, res.Intervals.Triage)
	res.Intervals.Workers = intervalOr(raw.intervals.workers, res.Intervals.Workers)
	res.Intervals.Reviewers = intervalOr(raw.intervals.reviewers, res.Intervals.Reviewers)
	res.Intervals.Merger = intervalOr(raw.intervals.merger, res.Intervals.Merger)
	for _, iv := range []struct {
		key string
		d   Duration
	}{
		{"intervals.scanners", res.Intervals.Scanners},
		{"intervals.triage", res.Intervals.Triage},
		{"intervals.workers", res.Intervals.Workers},
		{"intervals.reviewers", res.Intervals.Reviewers},
		{"intervals.merger", res.Intervals.Merger},
	} {
		if iv.d < minLoopInterval {
			errs = append(errs, &Error{File: cfgPath, Key: iv.key, Msg: fmt.Sprintf("must be at least 1m (the Reaper alone runs every minute), got %s", iv.d)})
		}
	}

	errs = append(errs, applyIntake(res, raw.intake, cfgPath)...)

	if raw.haveMerge {
		for i, p := range raw.protectedPaths {
			norm, err := normaliseProtectedPath(p)
			if err != nil {
				errs = append(errs, &Error{File: cfgPath, Key: fmt.Sprintf("merge.protected_paths[%d]", i), Msg: err.Error()})
				continue
			}
			res.ProtectedPaths = append(res.ProtectedPaths, norm)
		}
	}

	if raw.stale.inactiveFor != nil {
		res.Stale.InactiveFor = *raw.stale.inactiveFor
	}
	if raw.stale.suppressFor != nil {
		res.Stale.SuppressFor = *raw.stale.suppressFor
	}

	if raw.gitleaksEnabled != nil {
		res.Redact.Gitleaks.Enabled = *raw.gitleaksEnabled
	}
	if raw.gitleaksBlockAutoclose != nil {
		res.Redact.Gitleaks.BlockAutocloseWhenMissing = *raw.gitleaksBlockAutoclose
	}
	if raw.gitleaksPath != nil {
		if strings.TrimSpace(*raw.gitleaksPath) == "" {
			errs = append(errs, &Error{File: cfgPath, Key: "redact.gitleaks.path", Msg: "must not be empty"})
		} else {
			res.Redact.Gitleaks.Path = *raw.gitleaksPath
		}
	}

	for name, sc := range raw.sidecars {
		key := "sidecars." + name
		if name == "" || strings.ContainsAny(name, "/ \t") {
			errs = append(errs, &Error{File: cfgPath, Key: key, Msg: "sidecar name must be a simple key"})
		}
		entry := Sidecar{Ref: defaultSidecarRef}
		if sc.repo != nil {
			entry.Repo = *sc.repo
		}
		if sc.ref != nil {
			entry.Ref = *sc.ref
		}
		if _, _, err := repopath.Parts(entry.Repo); err != nil {
			errs = append(errs, &Error{File: cfgPath, Key: key + ".repo", Msg: fmt.Sprintf("must be %q, got %q", "owner/name", entry.Repo)})
		}
		if err := gitref.Check(entry.Ref); err != nil {
			errs = append(errs, &Error{File: cfgPath, Key: key + ".ref", Msg: fmt.Sprintf("invalid ref %q: %v", entry.Ref, err)})
		}
		res.Sidecars[name] = entry
	}

	return errs
}

// MaxLabelRunes is GitHub's label name limit, in characters.
const MaxLabelRunes = 50

// LabelRunes counts s the way GitHub limits label names: in characters.
func LabelRunes(s string) int { return utf8.RuneCountInString(s) }

// applyIntake merges and validates the intake setting. The prefix check uses
// the bare "ard:" prefix (invariant 4); the default label itself is spelled
// by package labels, so an unset Label stays "".
func applyIntake(res *Resolved, raw rawIntake, cfgPath string) Errors {
	var errs Errors
	if raw.mode != nil {
		res.Intake.Mode = IntakeMode(*raw.mode)
	}
	if m := res.Intake.Mode; m != IntakeAll && m != IntakeLabel {
		errs = append(errs, &Error{File: cfgPath, Key: "intake.mode", Msg: fmt.Sprintf("must be %q or %q, got %q", IntakeAll, IntakeLabel, string(m))})
	}
	if raw.label == nil {
		return errs
	}
	label := *raw.label
	res.Intake.Label = label
	bad := func(msg string) {
		errs = append(errs, &Error{File: cfgPath, Key: "intake.label", Msg: msg})
	}
	switch {
	case res.Intake.Mode == IntakeAll:
		bad("only valid when intake.mode is label")
	case label == "" || strings.TrimSpace(label) != label:
		bad("must not be empty or have leading or trailing whitespace")
	case !strings.HasPrefix(label, "ard:") || label == "ard:":
		bad(fmt.Sprintf("must start with %q and name a label, got %q", "ard:", label))
	case LabelRunes(label) > MaxLabelRunes:
		bad(fmt.Sprintf("must be at most %d characters (GitHub's limit)", MaxLabelRunes))
	case strings.Contains(label, ","):
		bad("must not contain a comma")
	}
	return errs
}

func intervalOr(set *Duration, def Duration) Duration {
	if set == nil {
		return def
	}
	return *set
}

// normaliseRelPath validates a repo-relative path (protected path, When
// path): no leading slash, no escaping "..", no empty or dot-only entries.
func normaliseRelPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path %q must be relative to the repo, not start with /", p)
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path %q escapes the repo", p)
	}
	return clean, nil
}

// DefaultMaxPods is the job slot count when no config has loaded yet.
const DefaultMaxPods = defaultMaxPods
