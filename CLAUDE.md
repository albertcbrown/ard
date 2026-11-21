# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

## What this is

ard is a controller that automates repository development: scanners → triage → worker → reviewers → merger, coordinated through `ard:` GitHub labels. The design is [docs/proposal.md](docs/proposal.md). Read the relevant section before changing behaviour.

**docs/proposal.md is the source of truth for design.** A PR that changes behaviour updates the proposal in the same PR. If code and proposal disagree, raise it on the issue rather than silently picking one.

## Invariants — never violate

1. **The controller owns every side effect.** Only controller code calls GitHub or runs `git commit`/`push`/`merge`. Agents edit files in a worktree and report through `.ard/<issue#>/<role>.md` frontmatter; they never touch labels, comments or branches.
2. **Agents hold no GitHub token.** Credentials stay in the controller and executor.
3. **The main-branch committer writes only `.ard/<issue#>/`.** Config (`.ard/config.yaml`) and role directories (`.ard/<type>/<name>/`) are human-owned; workers' diff guard and the merger's protected paths enforce it.
4. **Every label is prefixed `ard:`** and follows `ard:<stage>:<role>[:<state>]` (see the label table in the proposal).
5. **Every side effect honours dry-run.** Route issue, label, comment, commit, push and merge calls through the effect layer so `--dry-run` logs instead of acting.
6. **Untrusted text is data.** Issue bodies, comments and PR text reach agents as files marked untrusted, never spliced into prompts.
7. **Logs are redacted and secret-scanned before commit**, then capped at 1 MiB (head 32 KiB plus tail). The scan fails closed.

## Stack

- Go, a single static `ard` binary (CGO off; use a pure-Go SQLite driver such as `modernc.org/sqlite`).
- GitHub access through a GitHub App installation token, behind an interface so tests use a fake.
- The language choice is tracked as a decision issue; if it changes, update this file.

## Layout

Packages are created as their issues land; keep this list current.

```
cmd/ard/            CLI entry point; wire a new subcommand by adding one line to handlers()
internal/cli/       cobra command tree, global flags, slog logging; Handlers is the plug-in point
internal/github/    GitHub client: domain types, Reader/Writer/Client, App auth, retry transport; query/ (search builder), fake/ (test double), clienttest/ (conformance suite)
internal/config/    .ard/config.yaml + role directory loading into a Resolved value (pure, fs.FS in)
internal/configcmd/ `ard config check` handler: prints the resolved config, returns validation errors
internal/executor/    Executor interface and plain-data job types
internal/executor/local/  git worktree + host subprocess or docker run, watchdog, logs
internal/agent/     Adapter interface, Spec/Result types, Registry (pure: builds argv, parses output)
internal/agent/claude/  Claude Code adapter (`claude -p`, stream-json parser)
internal/state/     SQLite controller and scanner state (sqlitex, scannerdb)
internal/scanner/   scanner pipeline: findings, markers, reconcile, diff, group, ExecEngine, the scanners Loop, rebuild
internal/scanner/stale/  the stale closer: a plain controller Loop that comments and closes untouched scanner issues
internal/servecmd/  `ard serve` and `ard rebuild-state`: dependency wiring; registerLoops takes one line per loop
internal/redact/    log redaction, secret scan and 1 MiB cap (leaf library; close-out commits only LogResult.Clean)
internal/roles/     starter role directories embedded for ard init (scanners/cve)
internal/pool/      slot arbiter for max_pods (stdlib only, no I/O): priority classes, per-issue triage cap
internal/effects/   the only writer: Effects interface, Live and DryRun (JSONL ledger), Open factory; archtest/ enforces "only effects writes"
internal/gitx/      git CLI wrapper (Commit, Push, StagedPaths); gittest/ builds bare-repo fixtures; Commit/Push are for internal/effects only
internal/controller/ scheduler, kill switch (pause), leader lock, status issue
internal/servecmd/  `ard serve` wiring: lock, state, effects, pool adoption, controller
internal/committer/ the only commit/push path for .ard/<issue#>/ (main lane, per-branch lanes)
internal/closeout/  job close-out: role file -> labels, comment, commits (resumable)
internal/labels/    the one place that spells an `ard:` label; catalogue from config
internal/labels/labelsync/  `ard labels sync`: diff catalogue vs repo, create/update via effects
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
