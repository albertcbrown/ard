# Plan for #346

## Implementation

Decision on the open point from triage: this issue does **not** build the shared permission check. #302 owns `github.Connect` (not on main yet), so a second one here would conflict. Setup takes the check through a seam (`Deps.Permissions`) and this change wires a minimal default (repo readable with the configured auth). When #302 lands, one line in `cmd/ard/main.go` swaps in an adapter over `github.Connect`. The worker must not create `internal/github/connect.go`.

### Steps

1. New `internal/setupcmd/report.go`: `type Status string` with `StatusOK="ok"`, `StatusChanged="changed"`, `StatusDrift="drift"`, `StatusManual="manual"`; `type Row struct{ Name string; Status Status; Detail string }` (for manual, `Detail` is the exact fix); `type Report struct{ Rows []Row }` with `func (r Report) Green() bool` (true when every row is ok or changed... for the check phase, "green" means no drift and no manual), `func (r Report) HasDrift() bool`, and `func Print(w io.Writer, r Report, dryRun bool)` printing `<status>  <name>: <detail>` per row (`manual: <fix>` form for manual rows). Reason: one row model shared by checks and applies.
2. New `internal/setupcmd/setup.go`: `type Opts struct{ Check bool }` and `type Deps struct{ Reader labelsync.Reader; Writer labelsync.Writer; Desired []labels.Label; Permissions func(ctx context.Context) Row; LocalVsMain func(ctx context.Context) (warning string, err error); DryRun bool; Log *slog.Logger }`. `func Run(ctx context.Context, d Deps, o Opts, out io.Writer) (Report, error)`:
   - Phase 1 (always, in order): `LocalVsMain` warning (printed to out as `warning: ...`, never a row, never fails), `Permissions` row, then the labels row: compute `labelsync.Diff(have, d.Desired)`; no creates or updates gives `ok`, otherwise `drift` with a count (`3 to create, 1 to update`). Stale labels are listed in Detail but stay `ok` (sync never deletes).
   - `o.Check`: return the report; no writes.
   - Otherwise apply only when no row is `drift`-by-permission or `manual` (i.e. the permission row is `ok`). If the permission row is not ok, print the rows, skip the apply and return. Label drift alone does not block apply (that is what apply fixes).
   - Phase 2: `labelsync.Sync(ctx, d.Reader, d.Writer, d.Desired, d.Log)`; replace the labels row with `changed` (`created N, updated M`) or `ok`. Under `d.DryRun` the Writer is the effects dry-run, so nothing is written and the row says `would change`-style detail (`would create N, would update M`, status `changed`). A `Sync` failure with `rep.Failed` set becomes a `manual` row naming the failed labels; the error is returned.
   Reason: matches the order in docs/proposal.md (run every check, then apply only if green).
3. New `internal/setupcmd/cmd.go`: `func Command(ctx context.Context, e *cli.Env, o cli.SetupOpts) error`. Mirror `labelsync.Command` (`internal/labels/labelsync/cmd.go`): `config.RepoRoot(e.ConfigPath)`, `config.Load(root)`, `labels.Desired(labels.SpecFrom(res))`, repo from `--repo` or origin (the helper `resolveRepo` in labelsync is unexported: export it as `labelsync.ResolveRepo(ctx, e, root)` with its error text kept, and update its caller; or copy it, preferring the export), `github.AuthFromEnv`, `effects.Open(ctx, effects.Options{DryRun: e.DryRun, ...})`. Build `LocalVsMain` with `gitx.New("")`: if `origin/main` resolves (`RevParse`), `ChangedPaths(ctx, root, "origin/main", "HEAD", ".ard/")` plus a worktree check; any difference gives the warning `./.ard/ differs from origin/main (ard serve reads origin/main)`; a missing `origin/main` also warns, never errors. Default `Permissions`: a cheap `Reader.ListLabels` call: success is `ok` (`repository reachable`), failure is `manual` with `check ARD_GITHUB_* credentials and that the App is installed on <repo>` and no response body or token text. Map the result: `Check` with any `drift` returns `&cli.ExitError{Code: 2, Err: errors.New("setup: drift")}`; real errors exit 1.
4. `internal/cli/app.go`: add `type SetupOpts struct{ Check bool }` and `Setup func(ctx context.Context, e *Env, o SetupOpts) error // #346` to `Handlers`. New `internal/cli/cmd_setup.go` (pattern of `cmd_init.go`): `newSetup(app)` with `--check` bool flag, `Args: cobra.NoArgs`, nil handler gives `notImpl(cmd.CommandPath(), "#346")`. Register in `internal/cli/root.go` next to `newInit(app)`. `--dry-run` is already global (`Globals.DryRun`); `--check --dry-run` together is allowed (check wins, no writes).
5. `internal/cli/cmd_labels.go`: keep `ard labels sync` working unchanged as an alias (still `Handlers.LabelsSync`). Update its `Short` to say it is an alias of `ard setup` for labels only.
6. `cmd/ard/main.go` `handlers()`: add `Setup: setupcmd.Command`.
7. Docs: `docs/layout.md` add a line for `internal/setupcmd/`; `docs/proposal.md` needs no change (design is already under Configuration). No edits to `.github/`, `.ard/config.yaml` or other protected paths.

### Test plan

Command: `make test` (`go test ./...`), plus `make lint`.
- `internal/setupcmd/setup_test.go`, using `internal/github/fake` and `effects` fake or dry-run writers (read `internal/labels/labelsync/sync_test.go` for the pattern):
  - `TestRunAppliesWhenGreen`: empty repo labels, permission ok; labels created, report has `changed`.
  - `TestRunIdempotent`: second `Run` gives `ok` and zero writes on the fake.
  - `TestRunCheckReportsDriftNoWrites`: `Check: true` on an empty repo gives a `drift` row, `HasDrift()` true, fake has no labels afterwards.
  - `TestRunPermissionFailureSkipsApply`: `Permissions` returns `manual` with a fix; no label is written; the fix text is printed.
  - `TestRunDryRun`: dry-run Writer; nothing written, row detail says `would create`.
  - `TestRunNeverDeletes`: stale `ard:old` label remains and is listed.
  - `TestRunSyncFailureManualRow`: Writer errors for one label; `manual` row, error returned.
  - `TestRunOriginMainWarning`: `LocalVsMain` returns a warning; printed, not a row, does not change the outcome.
  - `TestPrintRows`: exact output for all four statuses.
- `internal/setupcmd/cmd_test.go`: `TestCheckExitsTwoOnDrift` (error is `*cli.ExitError` code 2), `TestCommandBadConfigBeforeNetwork`, and `TestLocalVsMain` against a temp git repo (same, differing and no `origin/main`).
- `internal/cli/commands_test.go` and `cli_test.go`: `ard setup` with a nil handler is not implemented; with a handler, `--check` reaches `SetupOpts{Check:true}`; `ard labels sync` still reaches `LabelsSync`; `ard help` lists `setup`.
- `cmd/ard/main_test.go`: extend the existing handler wiring test if it enumerates handlers.
- Edge cases: no origin remote (`--repo` required error text unchanged), 403 from the fake on `ListLabels`, a custom `intake.label`, case-only label name differences (labelsync treats as unchanged).

### Risk

Low to medium. New user-visible command and exit code 2. Writes only `ard:*` labels, through the effect layer (dry-run safe), and never deletes. No protected path from merge.protected_paths is touched. The permission check is a minimal placeholder until #302 lands; a maintainer should confirm the swap-in plan. No migrations or concurrency. Error text must never carry tokens or response bodies (the origin URL may embed a token: keep the labelsync wording).

### Size

M (about 250 to 400 changed lines including tests). Files: `internal/setupcmd/report.go`, `setup.go`, `cmd.go` and tests (all new), `internal/cli/app.go`, `internal/cli/cmd_setup.go` (new), `internal/cli/root.go`, `internal/cli/cmd_labels.go`, `internal/cli/commands_test.go`, `internal/labels/labelsync/cmd.go` (export `ResolveRepo`), `cmd/ard/main.go`, `docs/layout.md`.
