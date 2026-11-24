---
role: engineer
status: done
attempt: 1
---
Adds an optional, strictly parsed `fetched:` list to worker role files, carries it on the worker result, and renders it in the worker PR body under **Fetched inputs**. The proposal's egress and `network` text now matches the code.

## Changes
- `internal/closeout/rolefile.go`: `Fetched` type, `fetched:` parsing and validation (https-only URL, date, status 100..599, 64 lowercase hex sha256, `verified_by` URL or `none`, clean repo-relative path, at most 50 entries, worker stage only, type checks).
- `internal/closeout/worker.go`: `WorkerResult.Fetched`.
- `internal/worker/run.go`: keeps the list per job; drops it if redaction changes it or the secret scan hits (logs `worker.fetched_withheld`).
- `internal/worker/pr.go`: **Fetched inputs** list built from parsed fields only; absent when empty.
- `docs/proposal.md`: `fetched:` in the role file contract, corrected egress text (executors, worker, guardrails), per-executor `network` meaning, risks, residual risk, untrusted fetched bytes, spend-capped workspace key advice, CAPTURE.md convention.

## Testing
`go test ./internal/closeout ./internal/worker` passes. Added table tests `TestParseFetched` (valid, none, absent, 50/51 entries, each invalid field, unknown key, wrong types, entry number in message), `TestCheckFetchedStage` and `TestBodyFetched` (with and without entries). Grepped the proposal: the old egress phrases are gone.

## Open issues
- The seven risks from the tracking issue are not in the repository; I wrote a list of seven from general reasoning. A maintainer should check it against that issue.
- The reported line numbers did not match the file; I edited the egress text found by search. The Docker executor passes no network option, so the docs say nothing is enforced.
- No CAPTURE.md was added (none exists today). I added no loop-level test for the secret-scan withholding of `fetched`.
