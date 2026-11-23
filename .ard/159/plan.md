# Plan for #159

## Implementation

Decision (to be recorded in the proposal's worker section): keep the `type:*` label as the only source of the conventional prefix, and drop the prefix when the issue has no such label, giving `<issue title> (#<n>)`. A wrong `chore:` is worse than none. Taking the prefix from the lead's routing is rejected: it needs a new frontmatter key and an edit to the lead prompt under `.ard/triage/`, which agents may not change.

### Steps
1. `internal/worker/type.go`: change `CommitType` so the final fallback (no recognised `type:*` label) returns `""` instead of `"chore"`; update its doc comment. An unrecognised label such as `type:decision` also yields `""`. Reason: the root cause of the bad title.
2. `internal/committer/code.go`: in `CodeMessage`, accept an empty `typ` (the check at line 72 becomes `typ != "" && !typeRe.MatchString(typ)`); build `prefix` as `typ + ": "` only when `typ != ""`, else `""`. The length budget at line 87 already uses `prefix+suffix`, so it stays right. Update the `Type` comment on `CodeRequest` (line 29) to say empty means no prefix.
3. `internal/worker/pr.go`: no code change; `Title` goes through `CodeMessage`, so the PR title and commit title stay identical. `internal/worker/run.go` call sites (lines 532, 710) are unchanged.
4. `docs/proposal.md` line 295 (worker section, item 6): replace "and is `chore` otherwise" with: with no `type:*` label the prefix is dropped and the title is `<issue title> (#<issue>)`; the prefix is never taken from the plan or the lead's routing, so it depends on a maintainer applying `type:*` at intake. Also adjust the title form `<type>: <issue title> (#<issue>)` to say the `<type>: ` part is optional.

### Test plan
Command: `go test ./internal/worker/... ./internal/committer/...` (then `go test ./...`).
- `internal/worker/unit_test.go` `TestCommitType`: `nil` -> `""`; `{"type:decision"}` -> `""`; `{"area:x"}` -> `""` (new); the labelled cases are unchanged.
- `internal/worker/unit_test.go` Title test (near line 186): `Title("", "Crash on start", 7, "J1")` = `Crash on start (#7)` (new); the existing `fix` cases are unchanged.
- `internal/committer/code_test.go` `TestCodeMessage`: new case `typ: ""`, title `t` gives `t (#12)\n\nArd-Job: j1`; keep the `bad type` case (`Fix!`) invalid; add a long-title case with empty type that is truncated to `maxSummaryRunes`.
- Any test that expects `chore` for an unlabelled issue (search `chore` in `internal/worker/*_test.go`) is updated.

### Risk
Low. It touches the commit title format of the code lane, which is shared with `Branches.CommitCode`, but only for the previously invalid empty-type input. No protected paths, migrations or concurrency are affected. Anything that parses PR titles for a `chore:` prefix should be checked with a grep before merging.

### Size
S (about 30 changed lines): `internal/worker/type.go`, `internal/committer/code.go`, `internal/worker/unit_test.go`, `internal/committer/code_test.go`, `docs/proposal.md`.
