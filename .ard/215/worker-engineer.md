---
role: engineer
status: done
attempt: 1
---
Adds a scanner-level `route` key (pending, none, needs-human) that decides which label new scanner issues get, and a pre-filing secret guard that runs after every scan for every scanner and cannot be turned off.

## Changes
- internal/config: `Route` type and `route` override (defaults: pending for executable scanners, none for LLM scanners; bad values, `pending` on an LLM scanner and `route` on non-scanner roles are errors); `redact.gitleaks.block_autoclose_when_missing`; render output and golden file updated.
- internal/scanner/guard.go (new): `CheckFindings` scans title, fingerprint, group, files and body in one buffer with the builtin scanner, known secret values and an optional gitleaks pass. Results never carry scanned text.
- internal/scanner/pipeline.go: route check and route labels on create, on the dry-run synthetic issue and on joins. The stricter `joinableIssue` accepts only the scanner label plus the route label. The guard sits between scan and diff. A hit or builtin-pass failure records `scan_failed` and changes nothing. A gitleaks failure runs close-only autoclose unless blocked by config. `GuardRule` and `GuardField` are added to `RunResult`. `GuardScanners` and `Secrets` are test hooks.
- docs/proposal.md: guard, route, backpressure, grouping, filing, stale closer, gitleaks and configuration text.
- Tests: config, guard unit, pipeline guard and route tests, and a fixture test in internal/roles.

## Testing
Ran `go vet ./...` and `go test ./...`; all pass. Added `TestRoute`, the extended gitleaks config table, `TestCheckFindings*`, `TestGuardBlocks` (field × create/join/autoclose/rerate), dry-run, base-error, gitleaks-missing (stub and real missing binary, block on/off), cancelled, known-value (plain and base64), production-default, route create/bad/dry-run-join/autoclose, `TestJoinableIssue` and `TestFixturesPassTheGuard`.

## Open issues
- Stale closer: no new rows. The existing `TestDecide` rows "needs-human" and "not pending" (scanner label only) already pin that route none and needs-human issues are not candidates, so I added no `stale_test.go` run test.
- The gitleaks fixture variant is folded into `TestFixturesPassTheGuard` and runs when `ARD_GITLEAKS_BIN` is set, not as a separate integration-tagged file; it was not run here.
- `golangci-lint`/`make lint` was not run; only `gofmt` and `go vet`.
