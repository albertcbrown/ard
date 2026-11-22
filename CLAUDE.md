# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

## What this is

ard is a controller that automates repository development: scanners → triage → worker → reviewers → merger, coordinated through `ard:` GitHub labels. The design is [docs/proposal.md](docs/proposal.md). Read the relevant section before changing behaviour.

**docs/proposal.md is the source of truth for design.** A PR that changes behaviour updates the proposal in the same PR. If code and proposal disagree, raise it on the issue rather than silently picking one.

## Invariants — never violate

1. **The controller owns every side effect.** Only controller code calls GitHub or runs `git commit`/`push`/`merge`. Agents edit files in a worktree and report through `.ard/<issue#>/<stage>-<role>.md` frontmatter; they never touch labels, comments or branches.
2. **Agents hold no GitHub token.** Credentials stay in the controller and executor.
3. **The main-branch committer writes only `.ard/<issue#>/`.** Config (`.ard/config.yaml`) and role directories (`.ard/<type>/<name>/`) are human-owned. Only `ard init` may *propose* them, on the `ard-init` branch through `committer.Init` and a PR a human merges; it never writes main. Workers' diff guard, the committer's path guards and the merger's protected paths enforce it.
4. **Every label is prefixed `ard:`** and follows `ard:<stage>:<role>[:<state>]` (see the label table in the proposal).
5. **Every side effect honours dry-run.** Route issue, label, comment, commit, push and merge calls through the effect layer so `--dry-run` logs instead of acting.
6. **Untrusted text is data.** Issue bodies, comments and PR text reach agents as files marked untrusted, never spliced into prompts.
7. **Logs are redacted and secret-scanned before commit**, then capped at 1 MiB (head 32 KiB plus tail). The scan fails closed.

## Stack

- Go, a single static `ard` binary (CGO off; use a pure-Go SQLite driver such as `modernc.org/sqlite`).
- Supported platforms: macOS and Linux only; Windows is unsupported (the local executor relies on `flock` and POSIX process handling).
- GitHub access through a GitHub App installation token, behind an interface so tests use a fake.
- The language choice is tracked as a decision issue; if it changes, update this file.

## Layout

Packages are created as their issues land; keep this list current.

```
cmd/ard/            CLI entry point; wire a new subcommand by adding one line to handlers()
internal/cli/       cobra command tree, global flags, slog logging; Handlers is the plug-in point
internal/botcheck/  tests for the bot-commit audit in .github/workflows/bot-commits.yml
internal/github/    GitHub client: domain types, Reader/Writer/Client, App auth, retry transport; query/ (search builder), fake/ (test double), clienttest/ (conformance suite)
internal/config/    .ard/config.yaml + role directory loading into a Resolved value (pure, fs.FS in)
internal/configcmd/ `ard config check` handler: prints the resolved config, returns validation errors; `--ref` checks a commit
internal/configsrc/  config from origin/main: a snapshot per SHA under ~/.ard/config/<owner>/<repo>/ (what `ard serve` reads)
internal/executor/    Executor interface and plain-data job types
internal/executor/local/  git worktree + host subprocess or docker run, watchdog, logs
internal/agent/     Adapter interface, Spec/Result types, Registry (pure: builds argv, parses output)
internal/agent/claude/  Claude Code adapter (`claude -p`, stream-json parser)
internal/modelauth/ picks the one model credential (OAuth token or API key) from the controller env and carries its value to the executor; never logged
internal/state/     SQLite controller and scanner state (sqlitex, scannerdb)
internal/scanner/   scanner pipeline: findings, markers, reconcile, diff, group, ExecEngine, the scanners Loop, rebuild
internal/scanner/stale/  the stale closer: a plain controller Loop that comments and closes untouched scanner issues
internal/servecmd/  `ard serve` and `ard rebuild-state`: dependency wiring; registerLoops takes one line per loop
internal/redact/    log redaction, secret scan and 1 MiB cap (leaf library; close-out commits only LogResult.Clean)
internal/roles/     starter role directories embedded for ard init (scanners/cve, triage/lead)
internal/pool/      slot arbiter for max_pods (stdlib only, no I/O): priority classes, per-issue triage cap
internal/effects/   the only writer: Effects interface, Live and DryRun (JSONL ledger), Open factory; archtest/ enforces "only effects writes"
internal/gitx/      git CLI wrapper (Commit, Push, StagedPaths); gittest/ builds bare-repo fixtures; Commit/Push are for internal/effects only
internal/controller/ scheduler, kill switch (pause), leader lock, status issue
internal/servecmd/  `ard serve` wiring: lock, state, effects, pool adoption, controller
internal/committer/ the only commit/push path: .ard/<issue#>/ (main lane, per-branch lanes) and worker code on worker/ branches (CommitCode, CheckCodePath)
internal/closeout/  job close-out: role file or failure cause -> labels, comment, commits (resumable); every job ends through its Closer
internal/reaper/    the Reaper loop (failed-job detection, retention, :running with no record) and reaper.Finisher, the end-of-job path the loops and the Reaper share
internal/intake/    intake admission: Admit decides whether a human-filed issue may enter triage (pure; the triage loop calls it)
internal/triage/    the triage Loop: intake sweep, admission gate (body-hash re-gate), claim, watcher; closes out through the shared Finisher
internal/labels/    the one place that spells an `ard:` label; catalogue from config
internal/labels/labelsync/  `ard labels sync`: diff catalogue vs repo, create/update via effects
internal/gitref/    ref-name checks shared by config and executor; leaf, stdlib only
internal/repopath/  owner/repo to lowercased, nested local path segments (state, lock, work, logs); leaf, stdlib only
internal/initcmd/   ard init: proposes the starter .ard/ as a PR on ard-init
internal/roles/     starter .ard/ tree (config.yaml, role dirs) embedded for ard init
internal/           config, github, committer, executor, agent, scanner, state, ...
docs/proposal.md    design source of truth
```

New subcommands: implement the body in its own package as
`func(ctx context.Context, e *cli.Env) error`, then add one field to
`cli.Handlers` and one wiring line in `cmd/ard/main.go`'s `handlers()`.
A nil handler runs the command as a "not implemented yet" stub.

## Commands

```bash
make build   # bin/ard
make test    # go test ./...
make lint    # go vet + gofmt check
make fmt     # gofmt -w
```

Run `make lint test` before every commit.

## Conventions

- Tests: table-driven, no network. Use the GitHub fake for unit tests; real-API tests go behind the `integration` build tag.
- Errors: wrap with context (`fmt.Errorf("claim issue %d: %w", n, err)`); never swallow.
- Logging: structured (`log/slog`), one event per claim, close-out and failure.
- Keep packages small and interfaces at the consumer.

## Workflow

- Work is tracked in GitHub issues with `phase:N`, `area:*` and `type:*` labels and a phase milestone. Pick from the lowest open phase.
- Branch per issue: `<issue#>-<short-slug>`. Open a PR that says `Closes #<issue>`. Do not push to `main` directly.
- Keep PRs to one issue. Note follow-ups as new issues rather than widening the PR.
- Decision issues (`type:decision`) record the choice in a comment, then update the proposal and close.
- No AI attribution in commits or PRs: no `Co-Authored-By: Claude …` trailers, and no "Generated with Claude Code" lines in PR descriptions or comments.
