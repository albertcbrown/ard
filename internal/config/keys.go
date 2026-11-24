package config

// Keys returns every documented config key as a dotted path: the global
// keys in .ard/config.yaml and the per-role override keys. A map key is
// written as a placeholder (sidecars.<name>.repo). The user docs' key table
// is checked against this list, so a key added to the loader must be added
// here (TestKeysCoverRenderedConfig fails otherwise).
func Keys() []string {
	return []string{
		"max_pods", "executor", "agent", "require_approval",
		"budget_usd_per_day", "max_budget_usd", "global_max_open", "paused",
		"intervals.scanners", "intervals.triage", "intervals.workers",
		"intervals.reviewers", "intervals.merger", "intervals.prwatch",
		"pr_watch.pending_timeout", "roles_repo",
		"intake.mode", "intake.label",
		"merge.protected_paths",
		"stale.inactive_for", "stale.suppress_for",
		"redact.gitleaks.enabled", "redact.gitleaks.path",
		"redact.gitleaks.block_autoclose_when_missing",
		"sidecars.<name>.repo", "sidecars.<name>.ref",
		"overrides",
		// Per-role override keys.
		"every", "max_open", "group_size", "autoclose", "route",
		"model", "timeout", "sidecar", "after", "plan_section",
		"tests", "max_test_fixes", "max_review_rounds", "reviewers",
		"diff_warn_lines", "diff_max_lines",
	}
}
