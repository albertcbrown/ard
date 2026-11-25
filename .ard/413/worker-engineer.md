---
role: engineer
status: done
attempt: 1
---
Adds the in-process `Snapshot` model (no UI) in a new `internal/snapshot` package, plus the pool and controller data it needs. Taking a snapshot reads memory and SQLite only and never calls GitHub.

## Changes
- `internal/pool`: `Stats` gains `RefusedBy` (recent `limited` and `budget` refusals per role, 2 min TTL, display only) and `WaitingRoles` (slot demand per role). New `HeldSlots()`. Offer only records the refusal after the verdict is set, so grant decisions, `items` and demand are untouched.
- `internal/controller`: records each claiming loop's last `PauseState` and each loop's last tick (start, end, error). New `PauseStates()` and `Ticks()` return copies and never call the `PauseGate`.
- `internal/snapshot`: `Snapshot`, `Taker.Take` and `Invalidate`, source interfaces for fakes, utilization normalisation (fraction or percent), and a per-issue cost cache. Also `Ring` and the slog `NewTee` (forwards unchanged; optional redact func applies to the recorded copy only).
- `internal/servecmd`: wraps the serve logger with the tee and builds the Taker (`snapshot.go`), held on the wiring for #414. Nothing renders it, so serve output is unchanged.
- `docs/layout.md`: new package line and pool note.

## Testing
`go test ./...` passes; `go test -race ./internal/snapshot` passes. `go vet ./...` is clean for the changed packages. Added tests: pool refusals (reasons, no double count, expiry, no demand reserved), `HeldSlots`, `WaitingRoles`; controller pause/tick recording; snapshot table tests for every field, the finished-steps-only cost rule, the 00:00 UTC cut, the cache and `Invalidate`, source errors, nil sources; ring and tee (byte-identical output); a zero-GetIssue test through a real controller and `PauseGate`; an import test that the package does not import `internal/github`.

## Open issues
- `make lint` fails on `gofmt -l .` because of a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, which this change does not touch. The changed files are gofmt-clean.
- The parent design (#407) was not available. "Open `ard:` state" needs GitHub labels, so issues come from state only: running jobs plus jobs ended since 00:00 UTC. The state is the latest job's stage, role and status.
- Cache key is the issue's job ids and update times, and running issues are never cached, so close-out code is untouched. `Invalidate` exists for an explicit hook.
- The tee is wired with no redact function. Log lines already go to stderr unredacted, but a maintainer may want one added for the recorded copy.
- `snapshot` does not import `internal/github` directly. It imports `controller`, which does for its gate.
