---
role: engineer
status: done
attempt: 1
---
Adds an optional, strictly parsed `fetched:` list to worker role files, carries it on the worker result, lists it under **Fetched inputs** in the PR body only when non-empty, and corrects the egress and network text in the proposal.

## Changes
- `internal/closeout`: `Fetched` type, strict parse and validation (https URLs, date, status, 64 lowercase hex sha256, verified_by, clean repo-relative path, at most 50 entries, worker stage only); `WorkerResult.Fetched`. The landing path now also rejects control characters, backticks and angle brackets.
- `internal/worker`: `setFetched` applies redaction and a secret scan, and `Body` renders the **Fetched inputs** list from validated fields.
- `docs/proposal.md`: `fetched:` contract, corrected egress text, per-role `network` semantics, the seven risks, residual risk, untrusted fetched bytes, and a spend-capped key recommendation.
- Tests: `TestParseFetched` table, role-stage check, `TestBodyFetched`, and a worker loop test.

## Testing
Ran `go test ./internal/closeout ./internal/worker`; both pass. `gofmt -l` on the changed packages is clean. Added table cases for a path with a backtick, a newline and angle brackets.

## Review findings
- `internal/closeout/rolefile.go:8` (gofmt import order): fixed. The import order was already corrected in an earlier commit, and `gofmt -l` is clean.
- `internal/closeout/rolefile.go:346` (path markdown injection): fixed. The path check now rejects control characters, backticks, `<` and `>`, with three new table cases.
- `docs/proposal.md` (CAPTURE.md not present): not changed. The plan says this change only states the CAPTURE.md convention in the proposal. Creating the fixture-set files belongs to a later part. The worker prompt does not mention `fetched:` yet.

## Open issues
- `setFetched` has no dedicated unit test beyond the worker loop test.
