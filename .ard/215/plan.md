# Plan for #215

## Implementation

Incorporates the Opus plan review (amendments A1 to A12) and the owner's answers of 2026-11-23. Where an older note in #215 conflicts with this section, this section wins. Out of scope: widening the stale closer to `route: none` issues (#204); making `DecodeFindings` errors content-free (separate follow-up issue); any `internal/servecmd` change.

### Steps

1. **Config types (`internal/config/types.go`, which already exists; do not create a new file).**
   - Add `type Route string` with `RoutePending Route = "pending"`, `RouteNone Route = "none"`, `RouteNeedsHuman Route = "needs-human"`, and a field `Route Route` on `Role` next to `Autoclose` (scanner only; comment: default `pending` for executable scanners, `none` for LLM scanners).
   - Add `BlockAutocloseWhenMissing bool` to `Gitleaks` (comment: default false; when gitleaks is enabled but cannot run, also stop scanner autoclose for that run).

2. **`route` override (`internal/config/load.go`).**
   - `kindOverrideKeys[KindScanner]` gains `"route": true`. Add `route *string` to the override struct beside `autoclose`, and a `case "route":` in the decode switch beside `case "autoclose":` that uses `decodeValue`.
   - `applyRoleDefaults`, `KindScanner` case: `role.Route = RoutePending` when `role.ScanPath != ""`, else `RouteNone`.
   - `applyOverride`, when `ov.route != nil`: a value other than the three constants is `&Error{File: cfgPath, Key: keyRef + ".route", Msg: "must be pending, none or needs-human"}` (do not echo the value back beyond what other enum errors in load.go already do). `pending` on a role with `ScanPath == ""` is an error with key `keyRef + ".route"` (renders as `overrides["scanners/<name>"].route`) and message `LLM scanners may not use pending (a human adds ard:pending to one issue); use none or needs-human`. Overrides run after defaults (`applyRoleDefaults` before `applyOverride`), so this is the only place the check is needed. No separate `validateRoute` pass.
   - `route` on a triage, worker or reviewer role is already rejected by the `allowed` check through `wrongKindMsg` ("key \"route\" is only valid for scanners roles"); no code change, only a test.

3. **`block_autoclose_when_missing` (`internal/config/raw.go`, `defaults.go`, `render.go`), modelled on `redact.gitleaks.enabled` from #89.**
   - `raw.go`: field `gitleaksBlockAutoclose *bool // redact.gitleaks.block_autoclose_when_missing`; in `parseRedact` add `case "block_autoclose_when_missing":` decoding with `decodeValue(v, file, "redact.gitleaks.block_autoclose_when_missing", &raw.gitleaksBlockAutoclose)`; change the unknown-key message to `unknown key (want enabled, path or block_autoclose_when_missing)`.
   - `defaults.go`: default false (zero value, nothing to add to the `Redact{...}` literal); after the `gitleaksEnabled` block, `if raw.gitleaksBlockAutoclose != nil { res.Redact.Gitleaks.BlockAutocloseWhenMissing = *raw.gitleaksBlockAutoclose }`. A non-bool is an error from `decodeValue`. No cross-key check: the key is inert while `enabled` is false.
   - `render.go`: `gitleaksYAML` gains `BlockAutocloseWhenMissing bool \`yaml:"block_autoclose_when_missing"\`` (no omitempty, like `enabled`), set in the builder. The rendered role struct gains `Route string \`yaml:"route,omitempty"\`` after `Autoclose`, set from `string(role.Route)`.
   - Regenerate `internal/config/testdata/resolved.golden` with `go test ./internal/config -run Render -update` and check the diff shows only the new key and the `route:` lines.

4. **Guard (`internal/scanner/guard.go`, new).** `scanner` may import `internal/redact` (a stdlib-only leaf).

   ```go
   // GuardHit is one guard hit. It never carries scanned text.
   type GuardHit struct {
       Index int    // finding index in scan order
       Field string // title, fingerprint, group, files, body, or unknown
       Rule  string // builtin rule ID, "gitleaks:<id>" or "known-value:<ENV NAME>"
   }

   // GuardResult is the guard's verdict. It never carries scanned text.
   type GuardResult struct {
       Hits        []GuardHit // non-empty: the run changes nothing
       OptionalErr error      // the optional pass (gitleaks) could not run while ctx was live;
                              // set only when the base pass and known values were clean
   }

   // CheckFindings runs the pre-filing guard. The error is non-nil when the base pass
   // failed or ctx ended; it never contains scanned text.
   func CheckFindings(ctx context.Context, fs []Finding, base, optional redact.Scanner, sec *redact.Secrets) (GuardResult, error)
   ```

   - **One buffer (A1).** Build one `bytes.Buffer`: for each finding `i` in order, write each field's raw value followed by `"\n"`, in this order: `title`, `fingerprint`, `group` (skip when empty), each `files[j]` (field `files`), then `body` (skip when empty). No prefixes or labels (a prefix such as `token:` would change what `generic-assignment` and gitleaks match). Record `[]span{Index int; Field string; From, To int}` with 1-based lines, `To = From + strings.Count(value, "\n")`. A `redact.Finding.Line` outside every span maps to field `unknown` and still counts: mapping only labels the hit, it never decides whether it counts.
   - **Known values.** For each field value, `sec.Names([]byte(value))`; each name is a hit with rule `known-value:<NAME>` (the env var name; never the value). `sec` may be nil (no hits).
   - **Base pass.** A nil `base` is replaced by `redact.Builtin()` (no opt-out). Run it once over `bytes.NewReader(buf)` through a local copy of the `redact.Redactor.scan` goroutine/ctx-cancel pattern (it is unexported). An error, or `ctx.Err() != nil` afterwards, returns `(GuardResult{}, fmt.Errorf("guard: builtin pass: %w", err))`.
   - If known-value or base hits exist, return them sorted by `(Index, Field, Rule)` and do not run the optional pass.
   - **Optional pass.** When `optional != nil`, run it the same way over a fresh reader of the same bytes. Error with `ctx.Err() != nil`: return `(GuardResult{}, fmt.Errorf("guard: %w", ctx.Err()))`. Error with ctx live (missing binary, error exit, gitleaks' own timeout, unreadable report): return `GuardResult{OptionalErr: fmt.Errorf("guard: gitleaks pass: %w", err)}, nil`. Hits: map lines to spans and return them.
   - The guard's own error wrapping contains only fixed text and the `redact` error, which by contract carries no scanned text.

5. **Pipeline (`internal/scanner/pipeline.go`).**
   - **Hooks (A2).** Add to `Pipeline`, documented as test-only with nil meaning the full production guard:

     ```go
     // GuardScanners overrides the pre-filing guard's passes. Tests only. nil (production) =
     // base redact.Builtin(), optional redact.Gitleaks(redact.GitleaksOptions{Path: cfg.Redact.Gitleaks.Path})
     // when cfg.Redact.Gitleaks.Enabled, else no optional pass. There is no way to turn the guard off.
     GuardScanners func(cfg *config.Resolved) (base, optional redact.Scanner)
     // Secrets returns the controller's known values. nil (production) = redact.FromEnviron(os.Environ()), read every run.
     Secrets func() *redact.Secrets
     ```

     `internal/servecmd/scanners.go` is not changed (the reviewer's point: wiring there would make the nil default skip gitleaks).
   - **RunResult** gains `GuardRule, GuardField string`: the first hit's rule and field, or `scan-error` and `""` when a pass failed. No DB change; #221 can read them.
   - **Route check (A7)** in `Run`, after the existing LLM `scanner.skip` return and right after `record` is defined (store and snapshot loaded), before reconcile and backpressure: `routeLabel(r config.Route) (label string, ok bool)` maps `pending` to `labels.Pending`, `none` to `""`, `needs-human` to `labels.NeedsHuman`; anything else, including `""`, is `ok == false`. On `!ok`: `log.Warn("scanner.bad_route", "route", string(role.Route))` (the value comes from human-owned config), `record(OutcomeError)`, return `res, nil`; nothing is scanned, filed or closed.
   - **Guard placement (A3, A4).** Immediately after `res.Found = len(sr.Findings)` and before `Diff`, with the comment `// The pre-filing guard must stay between Engine.Scan and the first use of sr.Findings, for every engine (#204).` It runs on every run that reaches this point, including zero findings and dry-run (no `dry` condition).
   - **Outcomes (A5 and owner answer 3).** Let `gr, gerr := CheckFindings(...)`.
     - `gerr != nil && ctx.Err() != nil`: record nothing; `return res, errors.Join(append(errs, fmt.Errorf("scanner %s: guard: %w", role.Name, gerr))...)`. A shutdown is not a `scan_failed` streak for #221.
     - `gerr != nil` (base pass failed): log `scanner.guard_failed`, set `GuardRule = "scan-error"`, `record(OutcomeScanFailed)`, `return res, errors.Join(errs...)`.
     - `len(gr.Hits) > 0`: log hits (below), set `GuardRule`/`GuardField` from `gr.Hits[0]`, `record(OutcomeScanFailed)`, return as above.
     - `gr.OptionalErr != nil`: log `scanner.guard_failed`, set `GuardRule = "scan-error"`. If `cfg.Redact.Gitleaks.BlockAutocloseWhenMissing` or `!role.Autoclose`: `record(OutcomeScanFailed)` and return; nothing changes. Otherwise run **close-only autoclose** (below), then `record(OutcomeError)` if `ex.errs` is non-empty, else `record(OutcomeScanFailed)`, and return. No new run outcome.
     - Clean: the run continues exactly as today.
   - **Close-only autoclose.** `plan := Diff(snap, sr.Findings, true, start)`; build `ex` as today with a new field `closeOnly: true`; call only `ex.edits(plan, views)`. Skip grouping, joins, creates and `touchSeen`. In `edits`, when `e.closeOnly`, do not collect `plan.Resurface` or re-rates (`plan.Seen` severity changes). In `editIssue`, when `e.closeOnly`, return before any write unless `allChecked(lines)` after the gone ticks: an issue is only ticked when it is about to be closed, so nothing is partly edited. Hold and running-job deferrals are unchanged. The writes are then: tick gone lines on that issue's existing body, the "No longer detected" comment, the close, and the gone/fixed rows of findings this run did not report. None of them carries text from this run's findings.
   - **Logging (A6 as amended by owner answer 2).** Hits: one `log.Warn("scanner.guard_flagged", "finding", h.Index, "field", h.Field, "rule", h.Rule)` per hit, at most 20, then `log.Warn("scanner.guard_flagged_total", "hits", len(gr.Hits))`. Never the title, fingerprint, value or any field text. Pass failures: `log.Warn("scanner.guard_failed", "rule", "scan-error", "pass", "builtin"|"gitleaks", "reason", "timeout"|"error", "error", err.Error())`, with `reason = "timeout"` when `errors.Is(err, context.DeadlineExceeded)`; the gitleaks case adds `"autoclose", "allowed"|"blocked"`. Logging `err.Error()` is allowed for pass failures only, because `redact` errors carry no scanned text by contract.
   - **Route labels.** `create`: labels `labels.Scanner(name)` plus the route label when non-empty. `fetch` synthetic branch: the same list, so dry-run joins match. `execution` carries the route label (computed once in `Run`).
   - **Join semantics (A9).** `joinableIssue(lbls []string, scanner, routeLbl string, p Parsed, group string) bool`: open-issue checks unchanged (`HasSpan`, group); the issue's `ard:` labels, compared case-insensitively, must be exactly `{labels.Scanner(scanner)}` plus `routeLbl` when non-empty. Any other `ard:` label (including `ard:pending` under `none` or `needs-human`, `ard:hold`, any `:running`) makes it not joinable, so an issue a human promoted to triage is never appended to. Update both callers (`joinable`, `join`). `openIssues` stays keyed on the scanner label, so `max_open` counts every route.

6. **Stale closer (owner answer 1).** No code change: `stale.go`'s query and `decide.go`'s `precheck` require `ard:pending` and exclude `ard:needs-human`, so `none` and `needs-human` issues are never stale-closed. Lock this in with tests (Test plan). Widening is #204's job.

7. **Proposal (`docs/proposal.md`), all in this PR (A11).**
   - Label table row for `ard:needs-human` (line ~81): who sets it becomes "Controller, from the agent's result, or a scanner at filing (`route: needs-human`)"; keep the meaning column.
   - Scanners, after the strict-schema paragraph (~line 175): a **pre-filing guard** paragraph. It runs after every scan and before the diff, in dry-run too, even with zero findings; it scans title, fingerprint, group, files and body of every finding with the builtin pattern scanner and known controller secret values, plus gitleaks when `redact.gitleaks.enabled`; it cannot be turned off. A hit, a builtin-pass failure or an invalid line changes nothing: no issue is filed, joined, edited, ticked or closed, no finding row is written from the run's findings (reconcile and the dry-run ledger adoption, which read GitHub and the ledger, still run before the scan), and the run is recorded as `scan_failed`. When gitleaks is enabled but cannot run, nothing is filed, joined or edited, but autoclose of already-filed issues still proceeds (an issue is ticked only when it closes) unless `redact.gitleaks.block_autoclose_when_missing: true`; the run is still `scan_failed`. Logs name only the rule and field of a hit (known values by env var name); a pass failure also logs its error. A shutdown mid-guard records no run.
   - Scanners, a **`route`** paragraph beside it: values `pending` (default for executable scanners: `ard:pending`, enters triage), `none` (default for LLM scanners: scanner label only) and `needs-human` (`ard:needs-human`, never triaged); LLM scanners may not use `pending`; changing `route` affects only new issues; a human promotes one issue by removing `ard:needs-human` and/or adding `ard:pending`, after which ard never joins findings onto it.
   - Run step 2 (backpressure, ~line 180): `none` and `needs-human` issues count towards `max_open` and are never stale-closed, so a non-autoclose scanner with those routes stops when its issues fill `max_open` until a human closes or promotes some.
   - Step 5 (group): "not yet claimed" means no `ard:` label besides `ard:scanner:<name>` and the route's label. Step 6 (file): created with `ard:scanner:<name>` plus the route's label.
   - Stale-closer paragraph: `route: none` and `needs-human` issues lack `ard:pending` and are never candidates (#204 may widen this).
   - "Optional gitleaks pass (#89)" paragraph (~line 156): add `redact.gitleaks.block_autoclose_when_missing` (default false) and that the scanner guard also runs the pass.
   - Configuration (~line 422): add `route` to the per-role key list.
   - No credential-shaped literal anywhere in the proposal.

### Test plan

Command: `go test ./internal/config/... ./internal/scanner/... ./internal/roles/...`, then `make lint test` (with `GOCACHE` set to a writable dir). Build every secret-shaped string at run time from pieces (`"AK" + "IA" + ...`), as `internal/redact`'s tests do; never a credential-shaped literal (the worker diff guard holds literals).

- **Config (`load_test.go`, `render_test.go`).**
  - `TestRoute` table: each of the three values loads on an executable scanner; defaults (executable scanner `pending`, LLM scanner `none`); `route: sometimes` errors on the `.route` key; `route: pending` on an LLM scanner errors with the "LLM scanners may not use pending" message; `route: none` on a triage, worker and reviewer role errors with `only valid for scanners roles`.
  - Extend `TestLoadRedactGitleaks`: absent gives `BlockAutocloseWhenMissing: false`; `{enabled: true, block_autoclose_when_missing: true}` gives true; `{block_autoclose_when_missing: false}` gives false. Extend the error table: `block_autoclose_when_missing: [1]` errors on `redact.gitleaks.block_autoclose_when_missing`; the unknown-key case still errors.
  - Golden: `resolved.golden` shows `block_autoclose_when_missing: false` and the scanners' `route:`.
- **Guard unit (`internal/scanner/guard_test.go`).** Span mapping: a planted value in each of title, fingerprint, group, a second `files` entry and line 3 of a multi-line body maps to the right index and field; an empty `group` and empty `body` shift nothing; an out-of-range line from a stub scanner maps to `unknown` and still counts; a clean finding has no hits; base-pass hits skip the optional pass (stub records calls); an optional-pass error with ctx live is `OptionalErr` with a nil error; with ctx cancelled it is a returned error; a nil `base` still runs the builtin; `GuardHit` values and all returned error strings contain no planted text.
- **Pipeline (`pipeline_test.go`, `harness_test.go`).** Harness: `scannerRole` sets `Route: config.RoutePending`; the harness sets `Secrets: redact.NewSecrets`; guard tests use a capturing `slog` handler.
  - `TestGuardBlocks`, one table over field (title, body, files, fingerprint, group) × scenario: (a) a new finding that would create an issue; (b) a new finding in the group of an existing joinable issue; (c) the flagged finding is already filed (a `Seen` row) while another filed finding has gone and would autoclose its issue; (d) a severity change that would edit an issue body. Each asserts zero `CreateIssue`, `UpdateIssueBody`, `CreateComment` and `UpdateIssue` (`h.writes`), no committer call, every finding row unchanged, exactly one new run row `scan_failed`, the expected `RunResult.GuardField`, and that the whole captured log contains neither the planted value, nor the title, nor the fingerprint.
  - `TestGuardDryRun`: dry-run effects; the ledger has no `CreateIssue` record; outcome `scan_failed`.
  - `TestGuardBaseError`: a base stub that errors; `scan_failed`, `guard_failed` with `pass=builtin reason=error`, nothing written, including with zero findings plus a gone finding (autoclose blocked).
  - `TestGuardGitleaksMissing`, table over `block_autoclose_when_missing` false/true, with a clean base and an optional stub that errors (and one case with the real `redact.Gitleaks` at `/nonexistent/gitleaks` through a nil `GuardScanners` and `cfg.Redact.Gitleaks{Enabled: true, Path: ...}`). Setup: one filed issue whose only finding is gone, one filed issue with one gone and one still-present finding, one new finding, one re-rated finding. False: the fully gone issue is ticked, commented and closed; the partly gone issue is not edited; no create, join or re-rate; no `touchSeen` rows; outcome `scan_failed`, `Closed == 1`, log `autoclose=allowed`. True: zero writes, `autoclose=blocked`. Both: `GuardRule == "scan-error"`.
  - `TestGuardCancelled`: the stub cancels ctx inside `Scan`; nothing written, no run recorded, non-nil error.
  - `TestGuardKnownValue`: `Secrets.Add("ARD_TEST_TOKEN", v)` and `base64.StdEncoding.EncodeToString(v)` in the body; rule `known-value:ARD_TEST_TOKEN`; the log holds neither form of `v`.
  - `TestGuardProductionDefault`: nil `GuardScanners`, a built-at-run-time `AKIA…` key in the title; the run is blocked.
  - Route: `TestRouteCreate` table (pending: scanner + `ard:pending`; none: scanner only; needs-human: scanner + `ard:needs-human`); `TestRouteBad` (`Route: ""` and `"bogus"`: `scanner.bad_route`, `OutcomeError`, engine not called, no writes); `TestDryRunSyntheticJoinNeedsHuman` (second dry-run joins the synthetic issue); `TestAutocloseNeedsHuman` (all lines gone, the issue closes).
  - `TestJoinableIssue` (new): table over route × {exact labels, plus `ard:pending`, plus `ard:needs-human`, plus `ard:hold`, plus a `:running` label, a wrong group, no scanner label, mixed-case labels}.
- **Stale closer (`internal/scanner/stale/decide_test.go`).** Add rows to `TestDecide`: scanner label + `ard:needs-human` (no `ard:pending`) is not a candidate; scanner label only (route `none`) is not a candidate. In `stale_test.go`, one run against the fake with both kinds of issue inactive past `inactive_for` closes and comments on neither.
- **Fixtures (`internal/roles/scanner_guard_test.go`, new; `roles` already imports `scanner`).** Decode every `testdata/cve/*/want.jsonl` and `testdata/docs/*/want.jsonl` (replacing `@ROOT@` as `readFixture` does), run `scanner.CheckFindings(ctx, fs, redact.Builtin(), nil, redact.NewSecrets())`, expect no hits and no error. An `integration`-tagged variant passes `redact.Gitleaks` as the optional pass when `ARD_GITLEAKS_BIN` is set, like `gitleaks_integration_test.go`. error-codes has no golden output and is out of this test.

### Risk

Medium. It changes the shared pipeline every scanner uses (create labels, join check, a new early return and a close-only branch), so a mistake affects `cve` and `docs` filing; the route default `pending` keeps today's labels for every existing scanner, and `TestJoinableIssue` plus the pending-route create test pin that. The guard handles secret material: hits never carry or log text, and only pass failures log a `redact` error string. The close-only branch is the one place a failed guard still writes; it is limited to closing issues whose lines are all gone and writes no text from the run's findings, and `block_autoclose_when_missing: true` turns it off. With gitleaks enabled, each run starts one gitleaks subprocess (one buffer), not one per field. No migration, no new run outcome, no protected path (`.ard/config.yaml` and role directories untouched; `docs/proposal.md` is not protected).

### Size

L, about 950 changed lines including tests and docs (code ~330: config ~60, guard ~130, pipeline ~140; tests ~520; proposal ~100). Files: `internal/config/types.go`, `load.go`, `raw.go`, `defaults.go`, `render.go`, `load_test.go`, `testdata/resolved.golden`; `internal/scanner/guard.go` (new), `guard_test.go` (new), `pipeline.go`, `pipeline_test.go`, `harness_test.go`; `internal/scanner/stale/decide_test.go`, `stale_test.go`; `internal/roles/scanner_guard_test.go` (new) and its integration-tagged twin; `docs/proposal.md`. If the diff passes 1,000 lines, first fold the route and guard tests into fewer tables; if still over, defer in this order to a follow-up issue: the integration-tagged gitleaks fixture variant, then the `stale_test.go` run test (keep the `decide_test.go` rows). Do not defer the docs, the fixture test, the config key or `TestGuardGitleaksMissing`.
