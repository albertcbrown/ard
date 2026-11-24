# Plan for #292

## Implementation

### Steps

Names below: `.ard/PRODUCT.md`, `.ard/VISION.md`, `.ard/TECH_STACK.md` ("the three files"). The headings for each template come from the design in #291, which was not among the files available to the planner; the worker must read #291 and copy its heading list, not invent one.

1. **New** `internal/roles/PRODUCT.md`, `internal/roles/VISION.md`, `internal/roles/TECH_STACK.md`. Each holds only: a header HTML comment (what the file is for, "keep it under about 150 lines", and the line `Last reviewed: YYYY-MM-DD`), the #291 headings, and an HTML comment of guidance under each heading. No other prose. Reason: the embedded starter content.
2. `internal/roles/roles.go:14`: add `PRODUCT.md VISION.md TECH_STACK.md` to the `//go:embed` line, and name them in the `FS` doc comment. `starter()` in `internal/initcmd/plan.go` walks `roles.FS` and prefixes `.ard/`, so they become `InitFile`s with no change there.
3. `internal/roles/roles_test.go:34` (`TestFSContents`): add the three names to `want` in sorted order (`PRODUCT.md`, `TECH_STACK.md`, `VISION.md` sort before `config.yaml`, as uppercase precedes lowercase).
4. `internal/roles/config.yaml`: add `.ard/PRODUCT.md`, `.ard/VISION.md`, `.ard/TECH_STACK.md` to `merge.protected_paths` after `.ard/reviewers/` (explicit paths, no `.ard/*.md` glob). The list becomes 20 entries.
5. `internal/committer/init.go:32`: change `initPathRe` to also accept `PRODUCT\.md|VISION\.md|TECH_STACK\.md` after `\.ard/`, and update the `CheckInitPath` doc comment. Keep the numeric-role check working (`m[1]` is empty for these names).
6. `internal/initcmd`:
   - **New** exported `ContextPaths() []string` (or a package var) in `internal/initcmd/plan.go` returning the three paths, next to a new `actCreateIfAbsent` handling. Use it in `internal/initcmd/initcmd.go:154`: `ExportPaths(ctx, root, base, snap, append(config.SourcePaths(), ContextPaths()...)...)`. `config.SourcePaths()` stays unchanged (the issue says no config discovery change); the snapshot must contain the files only so `plan` can see them.
   - `plan()` in `plan.go`: for a file whose path is in `ContextPaths()`, if it exists on base in any form (any content, any mode) the op is `actUnchanged`; if absent it is `actCreate`. Never `actOverwrite`, never a conflict, whatever `force` is.
   - `overlay()` keeps working: it copies the base tree, then lays the planned files. Make sure it only lays files from `changed(ops)`-equivalent sets, or an existing differing context file would be replaced in the validation tree. Simplest: have `run` pass only non-`actUnchanged` files to `overlay` for context paths.
7. `internal/initcmd/body.go` `initBody`: add a "Before you merge" bullet telling adopters with an existing `.ard/config.yaml` to add the three paths to `merge.protected_paths` (the PR does not edit an existing config). Fixed text only.
8. `docs/proposal.md`:
   - The `.ard/` contract tree (around line 417) and the bullet list below it: add the three files with owner (human), purpose, optional, template-only counts as absent, precedence, the reviewer PR-head rule and conflict handling, as written in #291.
   - Merger protected-path list (around line 384): name the three files and change "17 entries" to 20.
   - Configuration example (around line 424): `merge.protected_paths` matches the new starter `config.yaml` order.
   - Guardrails "Role files are human-owned" (line 489): name the three files.
   - `ard init` paragraph (around line 460): say it proposes the three templates only when missing on main, never overwrites, even with `--force`.
9. Tests as in the Test plan below, including the `starterTree` update.

### Test plan

Command: `go test ./internal/roles ./internal/committer ./internal/initcmd ./internal/config ./internal/merger` then `go test ./...`.

- `internal/committer/codepath_test.go` `TestCheckCodePath`: add `.ard/PRODUCT.md`, `.ard/VISION.md`, `.ard/TECH_STACK.md` to `refused`. Expected: all rejected, no code change.
- `internal/committer/init_test.go` `TestCheckInitPath`: the three paths accepted; `.ard/OTHER.md`, `.ard/product.md`, `.ard/sub/PRODUCT.md`, `.ard/12/PRODUCT.md` rejected.
- `internal/initcmd/initcmd_test.go`:
  - `TestDefaultProtectedPaths` (line 534): length 20, update the index checks (`.mcp.json` moves to 15, the last entry to 19); add the three paths to `yes`; add `.ard/NOTES.md` and `docs/PRODUCT.md` to `no`, proving no glob.
  - Merger under the starter config: a test that loads the starter config and calls `res.Protected` for the three paths (same test, or a new `TestContextFilesProtected`).
  - `starterTree` (line 146): add the three `.ard/` paths in sorted position (`.ard/PRODUCT.md`, `.ard/TECH_STACK.md`, `.ard/VISION.md` come after `.ard/config.yaml`? the slice is sorted by `strings.Compare`, so uppercase sorts before `.ard/config.yaml`; check against `h.tree`'s order).
  - New `TestInitContextFiles`: repo lacking them gets all three in `ard-init`; repo with all three present with different content gets no op for them, with `Force` false and true (no conflict error, tree on `ard-init` lacks them or the run is "nothing to propose" if nothing else is missing); repo with one present and two absent proposes only the two.
  - `TestPlan`: cases for the context-path action (absent gives `actCreate`; present and different gives `actUnchanged` with force true and false).
  - Body test: `initBody` contains the add-to-`protected_paths` instruction.
- `internal/roles`: new `TestContextTemplates`: each of the three files, after removing HTML comments (`<!-- ... -->`, multi-line), contains only blank lines and `#` heading lines; the raw text has the `Last reviewed: YYYY-MM-DD` line, a size guidance mention of 150 lines, and the headings from #291. Failure case: a non-comment prose line fails the test.
- Existing tests to re-run, because the starter config changes: `internal/config` golden/load tests, `internal/roles/*_test.go`, merger tests.

### Risk

Medium-low. The changes are small and additive, but: `CheckInitPath` is a security gate on what `ard init` may commit, so the regex must stay exact and anchored; the init plan must never overwrite a human's context file, because `--force` is a destructive path; the three files become protected only through config, so existing adopters stay unprotected until they edit their config (the PR body says so). The merger, `CheckCodePath`, config discovery and `closeout.WritableFor` are not changed. Edits touch `internal/roles/config.yaml` (the embedded starter, not this repo's own `.ard/config.yaml`, which agents may not edit); no protected path in `.ard/config.yaml` is modified by the worker. No migrations, no concurrency.

### Size

M (about 200 to 300 changed lines, mostly templates, tests and docs). Files: `internal/roles/PRODUCT.md`, `internal/roles/VISION.md`, `internal/roles/TECH_STACK.md` (new), `internal/roles/roles.go`, `internal/roles/roles_test.go`, `internal/roles/config.yaml`, `internal/committer/init.go`, `internal/committer/init_test.go`, `internal/committer/codepath_test.go`, `internal/initcmd/plan.go`, `internal/initcmd/initcmd.go`, `internal/initcmd/body.go`, `internal/initcmd/initcmd_test.go`, `docs/proposal.md`.
