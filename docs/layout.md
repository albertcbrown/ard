# Layout

Every top-level directory and Go package, with what it holds. Keep it current: a PR that adds, removes or renames a directory or package updates this file in the same PR.

```
mkdocs.yml                  MkDocs Material config for the user docs site
docs/site/                  user docs (published to GitHub Pages)
cmd/ard/                    CLI entry point; wire a new subcommand by adding one line to handlers()
internal/cli/               cobra command tree, global flags, slog logging; Handlers is the plug-in point
internal/docscheck/         checks docs/site against the CLI commands and config keys (drift test)
internal/botcheck/          tests for the bot-commit audit in .github/workflows/bot-commits.yml
internal/versioncheck/       tests for the version workflow in .github/workflows/version.yml (plan and tag scripts)
internal/github/            GitHub client: domain types, Reader/Writer/Client, App auth, retry transport; query/ (search builder), fake/ (test double), clienttest/ (conformance suite)
internal/config/            .ard/config.yaml + role directory loading into a Resolved value (pure, fs.FS in)
internal/configcmd/         `ard config check` handler: prints the resolved config, returns validation errors; `--ref` checks a commit
internal/configsrc/         config from origin/main: a snapshot per SHA under ~/.ard/config/<owner>/<repo>/ (what `ard serve` reads)
internal/executor/          Executor interface and plain-data job types
internal/executor/local/    host subprocess or docker run, watchdog, tracking; calls workspace
internal/executor/workspace/ job worktree, home, config dir, inputs, sidecar export, logs and their removal; shared by local and kube
internal/agent/             Adapter interface, Spec/Result types, Registry (pure: builds argv, parses output)
internal/agent/claude/      Claude Code adapter (`claude -p`, stream-json parser)
internal/modelauth/         picks the one model credential (OAuth token or API key) from the controller env and carries its value to the executor; never logged
internal/state/             SQLite controller and scanner state (sqlitex, scannerdb)
internal/scanner/           scanner pipeline: findings, markers, reconcile, diff, group, ExecEngine, the scanners Loop, rebuild
internal/costblock/         the issue body's ard-owned cost block: render, strict parse, strip (admission), splice, per-issue Locks; leaf, stdlib only
internal/tarball/          safe streaming tar.gz pack/unpack through os.Root; leaf, stdlib only
internal/jobpod/           controller-pod contract of the k8s executor: paths, Secret items, input manifest, result route, key helpers; imports only executor and gitref
internal/costs/             the costs Loop: writes each dirty issue's cost table from state; Claims() false so it runs while paused
internal/scanner/stale/     the stale closer: a plain controller Loop that comments and closes untouched scanner issues
internal/agentjob/          helpers the agent loops share: untrusted-input snapshot, admission re-check, agent command and executor job builders, handle save, shutdown-aware wait, post-once
internal/worker/            the worker loop: claim, agent/test/fix steps, diff guard, code-lane push, draft PR, FinishWith
internal/servecmd/          `ard serve` and `ard rebuild-state`: dependency wiring (lock, state, effects, pool adoption, controller); registerLoops takes one line per loop
internal/redact/            log redaction (secrets and host paths), secret scan and 1 MiB cap (leaf library; close-out commits only LogResult.Clean)
internal/roles/             starter .ard/ tree (config.yaml, role dirs) embedded for ard init (scanners/cve, scanners/docs, scanners/error-codes, scanners/lint, scanners/missing-tests, scanners/trufflehog, scanners/trufflehog-history, triage/lead, triage/engineer, workers/engineer, reviewers/eng-review, reviewers/license-review, reviewers/security-review)
internal/pool/              slot arbiter for max_pods (stdlib only, no I/O): priority classes, per-issue triage cap
internal/effects/           the only writer: Effects interface, Live and DryRun (JSONL ledger), Open factory; archtest/ enforces "only effects writes"
internal/gitx/              git CLI wrapper (Commit, Push, StagedPaths, FileAt); gittest/ builds bare-repo fixtures; Commit/Push are for internal/effects only
internal/wtlock/            serialises git worktree add/remove/prune per repository within the process (git worktree bookkeeping is not concurrency-safe, #109); leaf
internal/controller/        scheduler, kill switch (pause), leader lock, status issue
internal/committer/         the only commit/push path: .ard/<issue#>/ (main lane, per-branch lanes) and worker code on worker/ branches (CommitCode, CheckCodePath)
internal/closeout/          job close-out: role file or failure cause -> labels, comment, commits (resumable); per-role write allowlist (WritableFor); every job ends through its Closer
internal/reaper/            the Reaper loop (failed-job detection, retention, :running with no record) and reaper.Finisher, the end-of-job path the loops and the Reaper share
internal/limits/            the book of exhausted usage limits the pool consults (the reactive half; #63 adds readings and thresholds)
internal/budget/            the daily dollar-cap gate the pool consults (budget.Gate: role and repo caps from step_usage, UTC day, fails closed)
internal/intake/            intake admission: Admit decides whether a human-filed issue may enter triage (pure; the triage loop calls it)
internal/prgate/            decisions shared by the reviewer, the merger and prwatch: reviews-passed-at-head (label rule, head Covers, reviewed heads), ParseWorkerBranch, and the check rule (EvalChecks, ResolveApps)
internal/janitor/           the janitor loop: prunes .ard/<issue#>/ of issues closed over 90 days ago through the main committer; no slot
internal/merger/            the merger loop: gates (reviews, checks from check runs and the rules API, protected-path approval), one update and one squash merge per tick; no slot
internal/prwatch/           the PR watch loop (#267): observes ard's PRs for closed-unmerged, long-pending checks, human merges and red drafts; labels, comments and events only, never merges; no slot
internal/reviewfix/         review fix rounds: the review_round event, Rounds/RequestRound, and the loop that sends :changes PRs back to the worker and reruns reviewers
internal/reviewer/          the reviewer loop: claims `ard:review:<role>` on ard's own PRs, pins the head, runs the agent on the diff, closes out through the shared Finisher (one PR review)
internal/triage/            the triage Loop: intake sweep, admission gate (body-hash re-gate), `after:` ordering, claim, watcher, fan-in gate (adds `ard:worker:<role>`); closes out through the shared Finisher
internal/labels/            the one place that spells an `ard:` label; catalogue from config
internal/labels/labelsync/  `ard labels sync`: diff catalogue vs repo, create/update via effects
internal/gitref/            ref-name checks shared by config and executor; leaf, stdlib only
internal/repopath/          owner/repo to lowercased, nested local path segments (state, lock, work, logs); leaf, stdlib only
internal/initcmd/           ard init: proposes the starter .ard/ as a PR on ard-init
Dockerfile                  the ard image (ghcr.io/albertcbrown/ard): ard plus the cve tools; no RUN on the target platform
deploy/argocd/              ard-root app-of-apps chart (one Application per target repo); deploy/charts/ard-serve/ one ard serve
deploy/test-*.sh            chart, image and repo checks for the above (`make chart-test`, `make image-test`); test-release.sh is `make release-check`
.goreleaser.yaml            GoReleaser v2 config: four-target archives, checksums and the Homebrew cask (checked by `make release-check`)
docs/proposal.md            design source of truth
docs/layout.md              this file: every directory and package
docs/TODO.md                open issues by phase, generated from the milestones
docs/runbooks/              operator runbooks (gate runs, live checks, repo onboarding)
```
