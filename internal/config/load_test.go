package config

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// baseFS is a fixture tree with one valid role of every shape plus issue
// directories that discovery must ignore.
func baseFS() fstest.MapFS {
	return fstest.MapFS{
		".ard/scanners/cve/scan":                    {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
		".ard/scanners/cve/osv.toml":                {Data: []byte("config"), Mode: 0o644},
		".ard/scanners/lint/prompt.txt":             {Data: []byte("lint the repo"), Mode: 0o644},
		".ard/triage/lead/prompt.txt":               {Data: []byte("route"), Mode: 0o644},
		".ard/triage/architect/prompt.txt":          {Data: []byte("design"), Mode: 0o644},
		".ard/triage/engineer/prompt.txt":           {Data: []byte("plan"), Mode: 0o644},
		".ard/workers/engineer/prompt.txt":          {Data: []byte("implement"), Mode: 0o644},
		".ard/workers/engineer/allowlist.txt":       {Data: []byte("ok"), Mode: 0o644},
		".ard/workers/engineer/scripts/test.sh":     {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
		".ard/reviewers/security-review/prompt.txt": {Data: []byte("security"), Mode: 0o644},
		".ard/reviewers/eng-review/prompt.txt":      {Data: []byte("eng"), Mode: 0o644},
		".ard/reviewers/license-review/prompt.txt":  {Data: []byte("license"), Mode: 0o644},
		// Issue directories: never role directories.
		".ard/12/issue.md": {Data: []byte("issue"), Mode: 0o644},
		".ard/007/x.md":    {Data: []byte("issue"), Mode: 0o644},
		// Numeric scanner directory: ignored like the issue dirs.
		".ard/scanners/99/prompt.txt": {Data: []byte("not a role"), Mode: 0o644},
	}
}

// withConfig returns base with .ard/config.yaml replaced.
func withConfig(t *testing.T, yaml string) fstest.MapFS {
	t.Helper()
	fsys := baseFS()
	for name := range fsys {
		if name == ".ard/config.yaml" {
			delete(fsys, name)
		}
	}
	fsys[".ard/config.yaml"] = &fstest.MapFile{Data: []byte(yaml), Mode: 0o644}
	return fsys
}

func mustLoad(t *testing.T, fsys fstest.MapFS) *Resolved {
	t.Helper()
	res, err := LoadFS(fsys, "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	return res
}

func loadErrs(t *testing.T, fsys fstest.MapFS) Errors {
	t.Helper()
	_, err := LoadFS(fsys, "/repo")
	if err == nil {
		t.Fatal("LoadFS succeeded, want errors")
	}
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("LoadFS error is %T, want config.Errors: %v", err, err)
	}
	return errs
}

func TestLoadDefaults(t *testing.T) {
	res := mustLoad(t, baseFS())

	if res.Repo != "/repo" {
		t.Errorf("Repo = %q, want /repo", res.Repo)
	}
	if res.MaxPods != 3 || res.Executor != "local" || res.Agent != "claude" ||
		!res.RequireApproval || res.BudgetUSDPerDay != 50 || res.MaxBudgetUSD != 5 ||
		res.GlobalMaxOpen != 30 || res.Paused {
		t.Errorf("globals = %+v", *res)
	}
	wantIntervals := Intervals{
		Scanners:  Duration(4 * time.Hour),
		Triage:    Duration(15 * time.Minute),
		Workers:   Duration(5 * time.Minute),
		Reviewers: Duration(5 * time.Minute),
		Merger:    Duration(5 * time.Minute),
	}
	if res.Intervals != wantIntervals {
		t.Errorf("Intervals = %+v, want %+v", res.Intervals, wantIntervals)
	}
	if res.Intake != (Intake{Mode: IntakeLabel}) {
		t.Errorf("Intake = %+v, want label mode with no label override", res.Intake)
	}
	if res.Stale.InactiveFor != Duration(30*24*time.Hour) || res.Stale.SuppressFor != Duration(90*24*time.Hour) {
		t.Errorf("Stale = %+v", res.Stale)
	}
	if len(res.Sidecars) != 0 || len(res.ProtectedPaths) != 0 {
		t.Errorf("Sidecars = %v, ProtectedPaths = %v", res.Sidecars, res.ProtectedPaths)
	}

	// Roles sort by kind, then name; numeric and issue dirs are absent.
	var gotNames []string
	for _, r := range res.Roles {
		gotNames = append(gotNames, kindDir[r.Kind]+"/"+r.Name)
	}
	wantNames := []string{
		"scanners/cve", "scanners/lint",
		"triage/architect", "triage/engineer", "triage/lead",
		"workers/engineer",
		"reviewers/eng-review", "reviewers/license-review", "reviewers/security-review",
	}
	if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") {
		t.Errorf("roles = %v, want %v", gotNames, wantNames)
	}

	cve, ok := res.Role(KindScanner, "cve")
	if !ok {
		t.Fatal("scanner cve not found")
	}
	if cve.ScanPath != ".ard/scanners/cve/scan" || cve.PromptPath != "" {
		t.Errorf("cve paths = scan %q prompt %q", cve.ScanPath, cve.PromptPath)
	}
	if !cve.Autoclose {
		t.Error("deterministic scanner must default autoclose to true")
	}
	if cve.Every != 4*time.Hour || cve.MaxOpen != 5 || cve.GroupSize != 10 {
		t.Errorf("cve scanner settings = every %s max_open %d group_size %d", cve.Every, cve.MaxOpen, cve.GroupSize)
	}
	if cve.Timeout != 30*time.Minute || cve.Agent != "claude" {
		t.Errorf("cve timeout/agent = %s %q", cve.Timeout, cve.Agent)
	}
	if strings.Join(cve.Extra, ",") != "osv.toml" {
		t.Errorf("cve extra = %v", cve.Extra)
	}

	lint, _ := res.Role(KindScanner, "lint")
	if lint.PromptPath != ".ard/scanners/lint/prompt.txt" || lint.ScanPath != "" {
		t.Errorf("lint paths = scan %q prompt %q", lint.ScanPath, lint.PromptPath)
	}
	if lint.Autoclose {
		t.Error("LLM scanner must default autoclose to false")
	}

	eng, _ := res.Role(KindWorker, "engineer")
	if eng.Tests != "make test" || eng.MaxTestFixes != 2 || eng.MaxReviewRounds != 3 {
		t.Errorf("worker defaults = tests %q fixes %d rounds %d", eng.Tests, eng.MaxTestFixes, eng.MaxReviewRounds)
	}
	if strings.Join(eng.Extra, ",") != "allowlist.txt,scripts/test.sh" {
		t.Errorf("worker extra = %v", eng.Extra)
	}

	lead, _ := res.Role(KindTriage, "lead")
	if lead.After != nil || lead.When != nil {
		t.Errorf("lead after/when = %v %v", lead.After, lead.When)
	}
	sec, _ := res.Role(KindReviewer, "security-review")
	if sec.When != nil {
		t.Errorf("security-review when = %+v, want nil (always)", sec.When)
	}
}

func TestLoadIntake(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want Intake
	}{
		{"all", "intake: {mode: all}\n", Intake{Mode: IntakeAll}},
		{"label, no label key", "intake: {mode: label}\n", Intake{Mode: IntakeLabel}},
		{"label kept verbatim", "intake: {mode: label, label: ard:pending}\n", Intake{Mode: IntakeLabel, Label: "ard:pending"}},
		{"custom label", "intake: {mode: label, label: ard:intake}\n", Intake{Mode: IntakeLabel, Label: "ard:intake"}},
		{"label alone defaults the mode", "intake: {label: ard:intake}\n", Intake{Mode: IntakeLabel, Label: "ard:intake"}},
		{"null", "intake:\n", Intake{Mode: IntakeLabel}},
		{"empty mapping", "intake: {}\n", Intake{Mode: IntakeLabel}},
		{"absent", "paused: false\n", Intake{Mode: IntakeLabel}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustLoad(t, withConfig(t, tt.yaml)).Intake; got != tt.want {
				t.Errorf("Intake = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadOverrides(t *testing.T) {
	fsys := withConfig(t, `
agent: kimi
intervals: {scanners: 6h, triage: 20m}
merge: {protected_paths: [infra/]}
sidecars:
  vpe-specs: {repo: acme/specs}
overrides:
  scanners/cve: {every: 12h, max_open: 10, group_size: 1, autoclose: false}
  scanners/lint: {timeout: 1h, model: claude-opus-5-5, max_budget_usd: 2, budget_usd_per_day: 7}
  triage/lead: {sidecar: vpe-specs, timeout: 15m, after: [architect]}
  workers/engineer: {tests: make ci-test, timeout: 60m, reviewers: [security-review, eng-review], max_test_fixes: 1, max_review_rounds: 4}
  reviewers/license-review: {agent: kimi, when: {paths: [go.mod, "package*.json"], triage: [architect]}}
`)
	res := mustLoad(t, fsys)

	if res.Agent != "kimi" || res.Intervals.Scanners != Duration(6*time.Hour) || res.Intervals.Triage != Duration(20*time.Minute) {
		t.Errorf("globals = %+v", *res)
	}
	if strings.Join(res.ProtectedPaths, ",") != "infra/" {
		t.Errorf("ProtectedPaths = %v", res.ProtectedPaths)
	}
	if sc := res.Sidecars["vpe-specs"]; sc.Repo != "acme/specs" || sc.Ref != "main" {
		t.Errorf("sidecar = %+v", sc)
	}

	cve, _ := res.Role(KindScanner, "cve")
	if cve.Every != 12*time.Hour || cve.MaxOpen != 10 || cve.GroupSize != 1 || cve.Autoclose {
		t.Errorf("cve override = every %s max_open %d group_size %d autoclose %v",
			cve.Every, cve.MaxOpen, cve.GroupSize, cve.Autoclose)
	}

	lint, _ := res.Role(KindScanner, "lint")
	// every inherits intervals.scanners; timeout, model and budgets come from the override.
	if lint.Every != 6*time.Hour {
		t.Errorf("lint every = %s, want 6h inherited from intervals.scanners", lint.Every)
	}
	if lint.Timeout != time.Hour || lint.Model != "claude-opus-5-5" || lint.MaxBudgetUSD != 2 || lint.BudgetUSDPerDay != 7 {
		t.Errorf("lint override = timeout %s model %q max_budget %g budget/day %g",
			lint.Timeout, lint.Model, lint.MaxBudgetUSD, lint.BudgetUSDPerDay)
	}

	lead, _ := res.Role(KindTriage, "lead")
	if lead.Sidecar != "vpe-specs" || lead.Timeout != 15*time.Minute || strings.Join(lead.After, ",") != "architect" {
		t.Errorf("lead = sidecar %q timeout %s after %v", lead.Sidecar, lead.Timeout, lead.After)
	}

	eng, _ := res.Role(KindWorker, "engineer")
	if eng.Tests != "make ci-test" || eng.Timeout != time.Hour ||
		strings.Join(eng.Reviewers, ",") != "security-review,eng-review" ||
		eng.MaxTestFixes != 1 || eng.MaxReviewRounds != 4 {
		t.Errorf("worker override = %+v", eng)
	}

	lic, _ := res.Role(KindReviewer, "license-review")
	if lic.Agent != "kimi" || lic.When == nil {
		t.Fatalf("license-review = agent %q when %v", lic.Agent, lic.When)
	}
	if strings.Join(lic.When.Paths, ",") != "go.mod,package*.json" || strings.Join(lic.When.Triage, ",") != "architect" {
		t.Errorf("license-review when = %+v", lic.When)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		fs   fstest.MapFS
		want []wantErr
	}{
		{
			name: "unknown top-level key",
			yaml: "frobnicate: true\n",
			want: []wantErr{{".ard/config.yaml", "frobnicate", "unknown key"}},
		},
		{
			name: "unknown override key",
			yaml: "overrides: {scanners/cve: {nope: 1}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["scanners/cve"].nope`, "unknown override key"}},
		},
		{
			name: "key valid only for another kind",
			yaml: "overrides: {workers/engineer: {every: 6h}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["workers/engineer"].every`, `only valid for scanners`}},
		},
		{
			name: "bad role keys",
			yaml: "overrides: {scanners/x/y: {}, Scanners/cve: {}, scanners/123: {}}\n",
			want: []wantErr{
				{".ard/config.yaml", `overrides["scanners/x/y"]`, "invalid key"},
				{".ard/config.yaml", `overrides["Scanners/cve"]`, "invalid key"},
				{".ard/config.yaml", `overrides["scanners/123"]`, "invalid key"},
			},
		},
		{
			name: "override names a missing directory",
			yaml: "overrides: {scanners/cvee: {every: 6h}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["scanners/cvee"]`, "no such role directory"}},
		},
		{
			name: "built-in stale scanner has no directory",
			yaml: "overrides: {scanners/stale: {every: 24h}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["scanners/stale"]`, "no such role directory"}},
		},
		{
			name: "integer where a duration string is required",
			yaml: "intervals: {triage: 15}\n",
			want: []wantErr{{".ard/config.yaml", "intervals.triage", `expected a duration string like "15m", got integer 15`}},
		},
		{
			name: "bad durations",
			yaml: "intervals: {scanners: -5m, workers: 1x, reviewers: 0s}\n",
			want: []wantErr{
				{".ard/config.yaml", "intervals.scanners", `invalid duration "-5m"`},
				{".ard/config.yaml", "intervals.workers", `invalid duration "1x"`},
				{".ard/config.yaml", "intervals.reviewers", `invalid duration "0s"`},
			},
		},
		{
			name: "intake is a scalar",
			yaml: "intake: all\n",
			want: []wantErr{{".ard/config.yaml", "intake", "must be a mapping with mode (all or label)"}},
		},
		{
			name: "intake unknown mode",
			yaml: "intake: {mode: some}\n",
			want: []wantErr{{".ard/config.yaml", "intake.mode", "must be \"all\" or \"label\", got \"some\""}},
		},
		{
			name: "intake numeric mode",
			yaml: "intake: {mode: 3}\n",
			want: []wantErr{{".ard/config.yaml", "intake.mode", "got \"3\""}},
		},
		{
			name: "intake mode wrong type",
			yaml: "intake: {mode: [a]}\n",
			want: []wantErr{{".ard/config.yaml", "intake.mode", "cannot unmarshal"}},
		},
		{
			name: "intake label with all mode",
			yaml: "intake: {mode: all, label: \"ard:x\"}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "only valid when intake.mode is label"}},
		},
		{
			name: "intake empty label",
			yaml: "intake: {label: \"\"}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "must not be empty"}},
		},
		{
			name: "intake label whitespace",
			yaml: "intake: {label: \" ard:x\"}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "must not be empty or have leading or trailing whitespace"}},
		},
		{
			name: "intake label without prefix",
			yaml: "intake: {label: pending}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "must start with \"ard:\""}},
		},
		{
			name: "intake label prefix only",
			yaml: "intake: {label: \"ard:\"}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "must start with \"ard:\""}},
		},
		{
			name: "intake label too long",
			yaml: "intake: {label: \"ard:" + strings.Repeat("é", 47) + "\"}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "at most 50 characters"}},
		},
		{
			name: "intake label comma",
			yaml: "intake: {label: \"ard:a,b\"}\n",
			want: []wantErr{{".ard/config.yaml", "intake.label", "must not contain a comma"}},
		},
		{
			name: "intake unknown key",
			yaml: "intake: {nope: 1}\n",
			want: []wantErr{{".ard/config.yaml", "intake.nope", "unknown key (want mode or label)"}},
		},
		{
			name: "bad executor",
			yaml: "executor: remote\n",
			want: []wantErr{{".ard/config.yaml", "executor", `must be "local" or "k8s"`}},
		},
		{
			name: "max_pods zero",
			yaml: "max_pods: 0\n",
			want: []wantErr{{".ard/config.yaml", "max_pods", "must be at least 1"}},
		},
		{
			name: "budget zero is not unlimited",
			yaml: "budget_usd_per_day: 0\n",
			want: []wantErr{{".ard/config.yaml", "budget_usd_per_day", "must be greater than 0"}},
		},
		{
			name: "loop interval under one minute",
			yaml: "intervals: {workers: 30s}\n",
			want: []wantErr{{".ard/config.yaml", "intervals.workers", "must be at least 1m"}},
		},
		{
			name: "scanner every under one minute",
			yaml: "overrides: {scanners/cve: {every: 45s}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["scanners/cve"].every`, "must be at least 1m"}},
		},
		{
			name: "unknown sidecar reference",
			yaml: "overrides: {triage/lead: {sidecar: vpe-specs}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["triage/lead"].sidecar`, "no such sidecar"}},
		},
		{
			name: "dangling after",
			yaml: "overrides: {triage/engineer: {after: [ghost]}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["triage/engineer"].after`, `no such triage role "ghost"`}},
		},
		{
			name: "dangling reviewer",
			yaml: "overrides: {workers/engineer: {reviewers: [ghost]}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["workers/engineer"].reviewers`, `no such reviewer role "ghost"`}},
		},
		{
			name: "dangling when.triage",
			yaml: "overrides: {reviewers/license-review: {when: {triage: [ghost]}}}\n",
			want: []wantErr{{".ard/config.yaml", `overrides["reviewers/license-review"].when.triage`, `no such triage role "ghost"`}},
		},
		{
			name: "protected path escapes the repo",
			yaml: "merge: {protected_paths: [../outside]}\n",
			want: []wantErr{{".ard/config.yaml", "merge.protected_paths[0]", "escapes the repo"}},
		},
		{
			name: "protected path names the whole repo",
			yaml: "merge: {protected_paths: [./]}\n",
			want: []wantErr{{".ard/config.yaml", "merge.protected_paths[0]", "names the whole repo"}},
		},
		{
			name: "protected prefix with glob characters",
			yaml: "merge: {protected_paths: [docs/*/]}\n",
			want: []wantErr{{".ard/config.yaml", "merge.protected_paths[0]", "contains glob characters"}},
		},
		{
			name: "protected invalid glob",
			yaml: "merge: {protected_paths: [\"src/[a\"]}\n",
			want: []wantErr{{".ard/config.yaml", "merge.protected_paths[0]", "invalid glob"}},
		},
		{
			name: "protected errors are collected",
			yaml: "merge: {protected_paths: [.github/, \"src/[a\", ./]}\n",
			want: []wantErr{
				{".ard/config.yaml", "merge.protected_paths[1]", "invalid glob"},
				{".ard/config.yaml", "merge.protected_paths[2]", "names the whole repo"},
			},
		},
		{
			name: "protected path has leading whitespace",
			yaml: "merge: {protected_paths: [\" .github/\"]}\n",
			want: []wantErr{{".ard/config.yaml", "merge.protected_paths[0]", "whitespace"}},
		},
		{
			name: "protected path must be relative",
			yaml: "merge: {protected_paths: [/etc/passwd]}\n",
			want: []wantErr{{".ard/config.yaml", "merge.protected_paths[0]", "must be relative"}},
		},
		{
			name: "explicit zero scanner caps",
			yaml: "overrides: {scanners/cve: {max_open: 0, group_size: 0}}\n",
			want: []wantErr{
				{".ard/config.yaml", `overrides["scanners/cve"].max_open`, "use paused"},
				{".ard/config.yaml", `overrides["scanners/cve"].group_size`, "use paused"},
			},
		},
		{
			name: "scanner with scan and prompt",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/scanners/both/scan"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755}
				fsys[".ard/scanners/both/prompt.txt"] = &fstest.MapFile{Data: []byte("both"), Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/scanners/both", "", "both scan and prompt.txt"}},
		},
		{
			name: "scanner without scan or prompt",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/scanners/empty/note.txt"] = &fstest.MapFile{Data: []byte("hi"), Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/scanners/empty", "", "needs an executable scan or a prompt.txt"}},
		},
		{
			name: "non-executable scan",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/scanners/cve/scan"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/scanners/cve", "scan", "must be executable"}},
		},
		{
			name: "empty prompt",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/triage/lead/prompt.txt"] = &fstest.MapFile{Data: nil, Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/triage/lead", "prompt.txt", "must not be empty"}},
		},
		{
			name: "scan outside scanners",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/triage/lead/scan"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755}
				return fsys
			}(),
			want: []wantErr{{".ard/triage/lead", "scan", "only scanners may have a scan script"}},
		},
		{
			name: "invalid role name",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/scanners/Bad_Name/prompt.txt"] = &fstest.MapFile{Data: []byte("x"), Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/scanners/Bad_Name", "", "invalid role name"}},
		},
		{
			name: "worker name with a trailing number",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/workers/eng-12/prompt.txt"] = &fstest.MapFile{Data: []byte("x"), Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/workers/eng-12", "", "worker role name must not contain an all-digit segment"}},
		},
		{
			name: "worker name with a middle number",
			fs: func() fstest.MapFS {
				fsys := baseFS()
				fsys[".ard/workers/eng-12-x/prompt.txt"] = &fstest.MapFile{Data: []byte("x"), Mode: 0o644}
				return fsys
			}(),
			want: []wantErr{{".ard/workers/eng-12-x", "", "worker role name must not contain an all-digit segment"}},
		},
		{
			name: "unknown interval key",
			yaml: "intervals: {scannerz: 4h}\n",
			want: []wantErr{{".ard/config.yaml", "intervals.scannerz", "unknown key"}},
		},
		{
			name: "bad stale duration",
			yaml: "stale: {inactive_for: 2x}\n",
			want: []wantErr{{".ard/config.yaml", "stale.inactive_for", `invalid duration "2x"`}},
		},
		{
			name: "unknown sidecar field",
			yaml: "sidecars: {specs: {repo: a/b, url: x}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.url", "unknown key"}},
		},
		{
			name: "bad sidecar repo",
			yaml: "sidecars: {specs: {repo: just-a-name}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.repo", `"owner/name"`}},
		},
		{
			name: "bad sidecar empty ref",
			yaml: "sidecars: {specs: {repo: a/b, ref: \"\"}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.ref", "must not be empty"}},
		},
		{
			name: "bad sidecar ref leading dash",
			yaml: "sidecars: {specs: {repo: a/b, ref: -x}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.ref", "must not start with"}},
		},
		{
			name: "bad sidecar ref colon",
			yaml: "sidecars: {specs: {repo: a/b, ref: \"a:b\"}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.ref", `":"`}},
		},
		{
			name: "bad sidecar ref double dot",
			yaml: "sidecars: {specs: {repo: a/b, ref: \"a..b\"}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.ref", `".."`}},
		},
		{
			name: "bad sidecar ref abbreviated sha",
			yaml: "sidecars: {specs: {repo: a/b, ref: abc1234}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.ref", "abbreviated SHA"}},
		},
		{
			name: "bad sidecar repo with space",
			yaml: "sidecars: {specs: {repo: \"a b/c\"}}\n",
			want: []wantErr{{".ard/config.yaml", "sidecars.specs.repo", `"owner/name"`}},
		},
		{
			name: "invalid YAML",
			yaml: "max_pods: [unclosed\n",
			want: []wantErr{{".ard/config.yaml", "", "invalid YAML"}},
		},
		{
			name: "top level must be a mapping",
			yaml: "- just\n- a\n- list\n",
			want: []wantErr{{".ard/config.yaml", "", "top level must be a mapping"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := tt.fs
			if fsys == nil {
				fsys = withConfig(t, tt.yaml)
			}
			errs := loadErrs(t, fsys)
			for _, want := range tt.want {
				assertErr(t, errs, want)
			}
			if len(errs) != len(tt.want) {
				t.Errorf("got %d errors, want %d: %v", len(errs), len(tt.want), errs)
			}
		})
	}
}

type wantErr struct {
	file, key, contains string
}

func assertErr(t *testing.T, errs Errors, want wantErr) {
	t.Helper()
	for _, e := range errs {
		if e.File == want.file && e.Key == want.key && strings.Contains(e.Msg, want.contains) {
			return
		}
	}
	t.Errorf("no error {file %q key %q msg containing %q} in:\n%s", want.file, want.key, want.contains, errs)
}

func TestHostUntrusted(t *testing.T) {
	fsys := baseFS()
	fsys[".ard/triage/architect/Dockerfile"] = &fstest.MapFile{Data: []byte("FROM scratch"), Mode: 0o644}
	res := mustLoad(t, fsys)
	var got []string
	for _, r := range res.HostUntrusted() {
		got = append(got, string(r.Kind)+"/"+r.Name)
	}
	// cve is a deterministic scanner and architect has a Dockerfile: neither
	// is listed. lint is an LLM scanner.
	want := []string{"scanner/lint", "triage/engineer", "triage/lead", "worker/engineer",
		"reviewer/eng-review", "reviewer/license-review", "reviewer/security-review"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("HostUntrusted = %v, want %v", got, want)
	}
	res.Executor = "k8s"
	if got := res.HostUntrusted(); len(got) != 0 {
		t.Errorf("executor k8s: HostUntrusted = %v, want none", got)
	}
}

func TestRoleLookupHelpers(t *testing.T) {
	res := mustLoad(t, baseFS())
	roles := res.RolesOf(KindScanner)
	if len(roles) != 2 || roles[0].Name != "cve" || roles[1].Name != "lint" {
		t.Errorf("RolesOf(scanner) = %v", roles)
	}
	if _, ok := res.Role(KindScanner, "ghost"); ok {
		t.Error("Role(scanner, ghost) found, want missing")
	}
}

func TestErrorFormatting(t *testing.T) {
	e := &Error{File: ".ard/config.yaml", Key: `overrides["scanners/cvee"]`, Msg: "no such role directory"}
	want := `.ard/config.yaml: overrides["scanners/cvee"]: no such role directory`
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
	noKey := &Error{File: ".ard/scanners/cve", Msg: "needs an executable scan or a prompt.txt"}
	if !strings.HasPrefix(noKey.Error(), ".ard/scanners/cve: needs") {
		t.Errorf("Error() = %q", noKey.Error())
	}
	errs := Errors{e, noKey}
	if !strings.Contains(errs.Error(), "\n") {
		t.Errorf("Errors.Error() = %q, want joined lines", errs.Error())
	}
	var _ error = errs
}

func TestLoadProtectedRoundTrip(t *testing.T) {
	res := mustLoad(t, withConfig(t, "merge: {protected_paths: [.github/, \"**/*.tf\", CODEOWNERS]}\n"))
	if got := strings.Join(res.ProtectedPaths, ","); got != ".github/,**/*.tf,CODEOWNERS" {
		t.Errorf("ProtectedPaths = %q", got)
	}
}

func TestSidecarSHARefAndFor(t *testing.T) {
	sha := strings.Repeat("0a", 20)
	res := mustLoad(t, withConfig(t, "sidecars: {specs: {repo: Acme/Specs, ref: "+sha+"}}\noverrides:\n  triage/lead: {sidecar: specs}\n"))
	lead, _ := res.Role(KindTriage, "lead")
	other, _ := res.Role(KindTriage, "engineer")
	tests := []struct {
		name   string
		role   Role
		wantOK bool
	}{
		{"role without sidecar", other, false},
		{"role with sidecar", lead, true},
	}
	for _, tc := range tests {
		sc, ok := res.SidecarFor(tc.role)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v", tc.name, ok)
		}
		if ok && (sc.Repo != "Acme/Specs" || sc.Ref != sha) {
			t.Errorf("%s: sidecar = %+v", tc.name, sc)
		}
		if !ok && sc != (Sidecar{}) {
			t.Errorf("%s: sidecar = %+v, want zero", tc.name, sc)
		}
	}
}

func TestWorkerNamesWithDigits(t *testing.T) {
	for name, ok := range map[string]bool{
		"eng2": true, "v2-eng": true, "engineer": true, "eng-12": false, "eng-12-x": false, "12-eng": false,
	} {
		fsys := baseFS()
		fsys[".ard/workers/"+name+"/prompt.txt"] = &fstest.MapFile{Data: []byte("x"), Mode: 0o644}
		_, err := LoadFS(fsys, "/repo")
		if (err == nil) != ok {
			t.Errorf("worker %q: err = %v, want ok=%v", name, err, ok)
		}
	}
	// Only workers are affected.
	fsys := baseFS()
	fsys[".ard/triage/eng-12/prompt.txt"] = &fstest.MapFile{Data: []byte("x"), Mode: 0o644}
	if _, err := LoadFS(fsys, "/repo"); err != nil {
		t.Errorf("triage role eng-12: %v", err)
	}
}
