---
role: engineer
status: done
attempt: 1
---
Close-out now merges a triage job's `plan.md` into the latest `main` copy at commit time, replacing only the job's own `##` section. A section another role committed after the job's base is no longer reverted. If the `main` copy cannot be merged, the job becomes `needs-human`.

## Changes
- `internal/committer` (`request.go`, `committer.go`, `errors.go`): optional `File.Merge` hook, called in `lane.attempt` against the freshly reset head, so it re-runs on every push retry. A merge error returns the new `ErrMerge`, which is not retried.
- `internal/closeout/plan_merge.go` (new): `mergeOwnSection` keeps the head's preamble and other sections, and replaces, appends or drops the own section. It errors if the head has a duplicate own heading or a heading-like line in the own section.
- `internal/closeout/closeout.go`: sets `Merge` on `.ard/<n>/plan.md` for triage jobs that own a section. On `ErrMerge` the plan becomes `needs-human` with a "plan.md conflict" reason.
- `docs/proposal.md`: "Who may write what" describes the behaviour.
- Tests: `plan_merge_test.go` (table), `closer_test.go` (Merge wiring, conflict gives needs-human), `committer_test.go` (Merge sees the latest head, merge error commits nothing).

## Testing
Ran `go vet` and `go test ./internal/closeout ./internal/committer`; all pass. `gofmt -l` is clean. I did not run `make test` or `make lint`.

## Open issues
- The closeout test harness uses a fake committer. The end-to-end "newer section kept" check is therefore split: the `mergeOwnSection` table covers the merge, and a committer test covers the hook seeing the latest head. There is no single test combining both.
- On a merge conflict only the issue is marked `needs-human`. The log is not committed, the same as the existing commit-failure path.
- `Merge` receives the file's own bytes as a third argument (the plan sketched a closure over them), which keeps the closure free of file reads.
