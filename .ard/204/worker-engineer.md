---
role: engineer
status: done
attempt: 1
---
Implements PR 1 of the LLM scanner engine plan: the filing rules for `prompt.txt` scanners, as pure code in config, scanner and the stale closer. Nothing runs LLM scanners yet; the skip in `Pipeline.Run` is left in place.

## Changes
- `internal/config`: `Role.LLMScanner()`, used by `ReadsUntrusted`. `autoclose: true` on an LLM scanner and `every` below 24h are config errors; an explicit `max_budget_usd <= 0` is an error. An LLM scanner's default `every` is raised to 24h, so unset configs still load. Golden file updated.
- `internal/scanner/llm.go`: `ParseLLMFingerprint`, `CheckLLMFingerprints` (content-free errors, max 20) and `LLMGrammarLines`. Not wired into `Run`.
- `internal/scanner/neardup.go`: `SimilarTitles` and `SkipNearDuplicates` (Jaccard 0.8, 3-token floor).
- `internal/scanner/pipeline.go`, `reconcile.go`: `IssueView.Title`, `RunResult.NearDup`, near-duplicate skipping for LLM scanners with a `scanner.near_duplicate` log, and the LLM footer on created issues whose route is not `pending` (kept once across joins).
- `internal/scanner/stale`: an issue whose only `ard:` label is its scanner label is now a candidate; one extra search per scanner, merged and de-duplicated.
- `docs/proposal.md`: one paragraph on the new rules.

## Testing
`go test ./...` passes. New tests cover config rules, fingerprint grammar, near-duplicates, footer (including a join) and the stale closer (decide table and a Run test). `make lint`: `go vet` is clean; the gofmt step trips on the existing fixture `internal/roles/testdata/docs/skips/src/_x.go`, which this change does not touch.

## Open issues
- The plan's "LLM scanner without `max_budget_usd` errors" is not implemented: a role inherits the validated global budget, so it can never be missing. Only an explicit non-positive value errors. Likewise, a short `every` is raised to 24h by default rather than rejected when unset; only an explicit value below 24h errors.
- Near-duplicate skipping and the footer are tested through helpers (`SkipNearDuplicates`, `createBody`), not through `Pipeline.Run`, because `Run` still skips LLM scanners.
- A scanner-only issue that also carries `ard:hold` or `ard:needs-human` is not a candidate (reason not-candidate rather than hold), which has the same outcome.
- No `internal/servecmd` or `ard config check` test was added; both use the same loader, which `internal/config` tests cover.
