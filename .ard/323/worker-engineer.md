---
role: engineer
status: done
attempt: 1
---
Conflict rounds, part B: when a PR conflicts with main, the worker resolves the merge with main and the committer pushes it as a merge commit whose second parent is main's tip. This round adds the tests the reviewer found missing for the worker flow and the reviewfix landing check.

## Changes
- Earlier rounds (already in the branch): `gitx` merge helpers and multi-parent commits, committer merge-parent lane and tree invariant, worker `conflict.go` and its wiring in `run.go`, reviewfix landing check, servecmd wiring, docs.
- `internal/worker/conflict_round_test.go` (new): tests for the conflict input (header, escaped names, path cap, mainTip from the remote), a clean merge (no input), a non-conflict round, `preMerge` count and path mismatch errors, a resolved round (agent sees markers, `conflict.md` replaces `review-feedback.md`, `MergeParent` is the fetched main tip), and the leftover-marker hold with nothing committed.
- `internal/reviewfix/harness_test.go`, `loop_test.go`: stub can answer "not on main" and fetch errors; `TestMergeIncludesSecondParentOnMain` covers second parent on main (true), not on main (false), fetch error, and head equal to base.

## Testing
`go test ./worker ./reviewfix` and `go vet` on both pass. No production code changed this round.

## Review findings
- conflict.go:42 (blocking): fixed: added the worker conflict tests listed above.
- reviewfix/loop.go:389 (blocking): fixed for the tests. not changed: the hard-coded `main`. The committer's `cfg.Branch` is documented as for the main-branch committer only, so for worker code the committer's default is also `main`, and the worker opens PRs against `main`; the names agree.
- servecmd/serve.go:738 (non-blocking): not changed: no wiring test, end-to-end fake test or prgate whitespace case was added, to keep within budget.
- committer/merge.go:31 (non-blocking): not changed, see the branch name note above.

## Open issues
- Not covered: the conflict.md backtick fence, redaction and UTF-8 truncation (conflict.md currently lists paths only, no hunks), diff-guard own-changes test, no-git-in-agent-tree test, workflow-rejection test, servecmd `Rounds` wiring test and the end-to-end case.
- The reviewer's lint note is a parse error in an untouched testdata file.
