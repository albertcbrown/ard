package config

import (
	"io"

	"gopkg.in/yaml.v3"
)

// WriteYAML renders the resolved configuration to w in the same schema
// .ard/config.yaml uses, so `ard config check` shows exactly what the
// controller will see. Durations render in the compact unit form.
func (r *Resolved) WriteYAML(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(r.render()); err != nil {
		return err
	}
	return enc.Close()
}

type resolvedYAML struct {
	Repo            string                 `yaml:"repo"`
	SHA             string                 `yaml:"sha,omitempty"`
	MaxPods         int                    `yaml:"max_pods"`
	Executor        string                 `yaml:"executor"`
	Agent           string                 `yaml:"agent"`
	RequireApproval bool                   `yaml:"require_approval"`
	BudgetUSDPerDay float64                `yaml:"budget_usd_per_day"`
	MaxBudgetUSD    float64                `yaml:"max_budget_usd"`
	GlobalMaxOpen   int                    `yaml:"global_max_open"`
	Paused          bool                   `yaml:"paused"`
	Intervals       intervalsYAML          `yaml:"intervals"`
	Intake          intakeYAML             `yaml:"intake"`
	ProtectedPaths  []string               `yaml:"protected_paths,omitempty"`
	Stale           staleYAML              `yaml:"stale"`
	PRWatch         prWatchYAML            `yaml:"pr_watch"`
	Redact          redactYAML             `yaml:"redact"`
	Sidecars        map[string]sidecarYAML `yaml:"sidecars,omitempty"`
	Roles           []roleYAML             `yaml:"roles,omitempty"`
}

type intervalsYAML struct {
	Scanners  Duration `yaml:"scanners"`
	Triage    Duration `yaml:"triage"`
	Workers   Duration `yaml:"workers"`
	Reviewers Duration `yaml:"reviewers"`
	Merger    Duration `yaml:"merger"`
	PRWatch   Duration `yaml:"prwatch"`
}

type intakeYAML struct {
	Mode  IntakeMode `yaml:"mode"`
	Label string     `yaml:"label,omitempty"`
}

type staleYAML struct {
	InactiveFor Duration `yaml:"inactive_for"`
	SuppressFor Duration `yaml:"suppress_for"`
}

type prWatchYAML struct {
	PendingTimeout Duration `yaml:"pending_timeout"`
}

type redactYAML struct {
	Gitleaks gitleaksYAML `yaml:"gitleaks"`
}

type gitleaksYAML struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`

	BlockAutocloseWhenMissing bool `yaml:"block_autoclose_when_missing"`
}

type sidecarYAML struct {
	Repo string `yaml:"repo"`
	Ref  string `yaml:"ref"`
}

type roleYAML struct {
	Name       string   `yaml:"name"`
	Kind       Kind     `yaml:"kind"`
	Dir        string   `yaml:"dir"`
	Prompt     string   `yaml:"prompt,omitempty"`
	Scan       string   `yaml:"scan,omitempty"`
	Dockerfile string   `yaml:"dockerfile,omitempty"`
	Extra      []string `yaml:"extra,omitempty"`

	Agent   string   `yaml:"agent"`
	Model   string   `yaml:"model,omitempty"`
	Timeout Duration `yaml:"timeout"`
	Sidecar string   `yaml:"sidecar,omitempty"`

	Every     Duration `yaml:"every,omitempty"`
	MaxOpen   int      `yaml:"max_open,omitempty"`
	GroupSize int      `yaml:"group_size,omitempty"`
	Autoclose bool     `yaml:"autoclose,omitempty"`
	Route     string   `yaml:"route,omitempty"`

	After       []string `yaml:"after,omitempty"`
	PlanSection string   `yaml:"plan_section,omitempty"`

	Tests           string   `yaml:"tests,omitempty"`
	MaxTestFixes    int      `yaml:"max_test_fixes,omitempty"`
	MaxReviewRounds int      `yaml:"max_review_rounds,omitempty"`
	Reviewers       []string `yaml:"reviewers,omitempty"`
	DiffWarnLines   int      `yaml:"diff_warn_lines,omitempty"`
	DiffMaxLines    int      `yaml:"diff_max_lines,omitempty"`

	When *whenYAML `yaml:"when,omitempty"`

	MaxBudgetUSD    float64 `yaml:"max_budget_usd,omitempty"`
	BudgetUSDPerDay float64 `yaml:"budget_usd_per_day,omitempty"`
}

type whenYAML struct {
	Paths  []string `yaml:"paths,omitempty"`
	Triage []string `yaml:"triage,omitempty"`
}

func (r *Resolved) render() resolvedYAML {
	out := resolvedYAML{
		Repo:            r.Repo,
		SHA:             r.SHA,
		MaxPods:         r.MaxPods,
		Executor:        r.Executor,
		Agent:           r.Agent,
		RequireApproval: r.RequireApproval,
		BudgetUSDPerDay: r.BudgetUSDPerDay,
		MaxBudgetUSD:    r.MaxBudgetUSD,
		GlobalMaxOpen:   r.GlobalMaxOpen,
		Paused:          r.Paused,
		Intervals: intervalsYAML{
			Scanners:  r.Intervals.Scanners,
			Triage:    r.Intervals.Triage,
			Workers:   r.Intervals.Workers,
			Reviewers: r.Intervals.Reviewers,
			Merger:    r.Intervals.Merger,
			PRWatch:   r.Intervals.PRWatch,
		},
		Intake:         intakeYAML{Mode: r.Intake.Mode, Label: r.Intake.Label},
		ProtectedPaths: r.ProtectedPaths,
		Stale: staleYAML{
			InactiveFor: r.Stale.InactiveFor,
			SuppressFor: r.Stale.SuppressFor,
		},
		PRWatch:  prWatchYAML{PendingTimeout: r.PRWatch.PendingTimeout},
		Redact:   redactYAML{Gitleaks: gitleaksYAML{Enabled: r.Redact.Gitleaks.Enabled, Path: r.Redact.Gitleaks.Path, BlockAutocloseWhenMissing: r.Redact.Gitleaks.BlockAutocloseWhenMissing}},
		Sidecars: map[string]sidecarYAML{},
	}
	for name, sc := range r.Sidecars {
		out.Sidecars[name] = sidecarYAML{Repo: sc.Repo, Ref: sc.Ref}
	}
	for _, role := range r.Roles {
		ry := roleYAML{
			Name:            role.Name,
			Kind:            role.Kind,
			Dir:             role.Dir,
			Prompt:          role.PromptPath,
			Scan:            role.ScanPath,
			Dockerfile:      role.Dockerfile,
			Extra:           role.Extra,
			Agent:           role.Agent,
			Model:           role.Model,
			Timeout:         Duration(role.Timeout),
			Sidecar:         role.Sidecar,
			Every:           Duration(role.Every),
			MaxOpen:         role.MaxOpen,
			GroupSize:       role.GroupSize,
			Autoclose:       role.Autoclose,
			Route:           string(role.Route),
			After:           role.After,
			PlanSection:     role.PlanSection,
			Tests:           role.Tests,
			MaxTestFixes:    role.MaxTestFixes,
			MaxReviewRounds: role.MaxReviewRounds,
			Reviewers:       role.Reviewers,
			DiffWarnLines:   role.DiffWarnLines,
			DiffMaxLines:    role.DiffMaxLines,
			MaxBudgetUSD:    role.MaxBudgetUSD,
			BudgetUSDPerDay: role.BudgetUSDPerDay,
		}
		if role.When != nil {
			ry.When = &whenYAML{Paths: role.When.Paths, Triage: role.When.Triage}
		}
		out.Roles = append(out.Roles, ry)
	}
	return out
}
