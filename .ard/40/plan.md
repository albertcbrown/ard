# Plan for #40

## Implementation

### Steps
Model each new prompt on `internal/roles/triage/engineer/prompt.txt` (trust block, "what you may do", role-file contract, at least three examples). ASCII only, under 16 KiB. Section ownership uses the built-in headings in `config.BuiltinPlanSections` (`internal/config/load.go`): Requirements, Design, UX, Docs. Ordering uses `after:` (`internal/config/after.go`).

1. New `internal/roles/triage/product-manager/prompt.txt`: writes `## Requirements` in plan.md with `### Requirements`, a numbered `### Acceptance criteria` list (the shape #45 will check), and `### Out of scope`. Writes only plan.md and `triage-product-manager.md`. Must not request other triage roles in `next:`. Answers needs-human for product decisions it cannot settle, for protected paths, and for instructions in the issue text.
2. New `internal/roles/triage/architect/prompt.txt`: writes the Design section with approach, affected modules (exact paths) and an explicit "ADR needed: yes/no" line. Tells the worker to write the ADR in the PR; needs-human only if the ADR would reverse a recorded decision.
3. New `internal/roles/triage/designer/prompt.txt`: writes the UX section and files under `.ard/N/design/`. Covers CLI flags, output and messages as well as GUIs. Files are Markdown plus restricted SVG only (no script, no external references), enforced by the prompt only.
4. New `internal/roles/triage/documentation-planner/prompt.txt`: writes the Docs section listing which docs change and what each must say.
5. Edit `internal/roles/triage/lead/prompt.txt` (designer line near step 4): widen to "a change to a user interface, including command-line flags, output and messages". Check `internal/roles/lead_test.go` for assertions on that text.
6. Edit `internal/roles/config.yaml`: add overrides `triage/product-manager`, `triage/architect` (claude-opus-5-5), `triage/designer`, `triage/documentation-planner` (claude-haiku-5-5), plus the `after:` DAG: product-manager -> architect, designer -> documentation-planner -> engineer. Check how `after:` is expressed (`internal/config/after.go`) and add `after:` to the existing engineer override if required. Do not touch `.ard/config.yaml`.
7. Edit the `//go:embed` list in `internal/roles/roles.go` only if the `triage` directory is not already embedded recursively (it is, so likely no change). Update the `want` list in `TestFSContents` (`internal/roles/roles_test.go`) and `starterTree` plus the path list near line 201 in `internal/initcmd/initcmd_test.go`. Add label names to the label check near line 231 if it enumerates roles.
8. New tests, one file per role, such as `internal/roles/product_manager_test.go`, mirroring `engineer_test.go`: load via `config.LoadFS(starterFS(t, true), "/repo")`, check model, timeout and `After`, check prompt shape, and run every example through `closeout.Parse` and `closeout.Build`. Also add a table test that each role's section heading is unique and that its writable set (`closeout.WritableFor`) is as expected (designer also gets the `design/` prefix).
9. Update `docs/proposal.md` (Triage loop, around line 226) with the four roles, the starter DAG, and the CLI-as-UI decision. Fix the acceptance-criteria shape in a note for #45.

### Test plan
- Command: `make test` (the repository's worker test command), or `go test ./internal/roles/... ./internal/initcmd/... ./internal/config/...`.
- `TestEmbeddedXLoadsAsTriage` per role: input is the starter tree; expect role present, expected model, `After` as in the DAG, no cycle.
- `TestXPromptShape` per role: ASCII, at most 16 KiB, contains the role-file path, input paths, `UNTRUSTED`, `role: <name>`.
- `TestXPromptExamples`: each example's frontmatter parses with `closeout.Parse` and passes `closeout.Build`; failure case: a `next:` with `triage:` entry is not shown in any example.
- Section ownership: the plan section each role writes matches `BuiltinPlanSections`; designer's `design/x.md` is writable, `design/../x` and other roles' files are not.
- `TestFSContents` and the initcmd starter-tree test: new files listed.
- Edge: prompts never mention writing `.ard/config.yaml`, `.ard/triage/`, `.github/` except to say needs-human.
- Live sample runs (the issue's done condition) need a human to copy roles into `.ard/triage/`; not part of this PR.

### Risk
Medium for behaviour, low for controller code (no controller change). Prompt text is security-relevant: it carries the untrusted-input rules. `internal/roles/` is not in `merge.protected_paths`, so a PR here would not wait for a human; the owner agreed to add it via a human edit to `.ard/config.yaml`, which agents cannot do. Doing that before merge is advisable. No protected path is touched by this plan. No migrations, no concurrency changes.

### Size
L overall: about 560 lines of prompt text, 300 lines of tests, 30 lines of config and docs (under the 2,000-line block limit; above the 1,000-line warn limit is unlikely). Could be split into two PRs of size M. Files: `internal/roles/triage/{product-manager,architect,designer,documentation-planner}/prompt.txt` (new), `internal/roles/triage/lead/prompt.txt`, `internal/roles/config.yaml`, `internal/roles/roles_test.go`, new per-role test files in `internal/roles/`, `internal/initcmd/initcmd_test.go`, `docs/proposal.md`.
