---
role: engineer
status: done
attempt: 1
---
Retries transient push and fetch failures (5xx, network, token mint errors) in the committer main, code and init lanes with bounded backoff, checks whether a push already landed, and ends an exhausted job with a retryable `push_transient` close-out cause instead of `failed`.

## Changes
- `internal/gitx`: new `ErrTransient`; stderr classification for push and fetch (stale lease, non-fast-forward, ref-not-found and repo-access still win); token mint errors classified through `github.IsTransientToken`.
- `internal/github/auth.go`: new `IsTransientToken` (network error, unexpected EOF, or 5xx from ghinstallation or go-github). Fix round: gitx previously imported go-github directly, which the effects architecture test forbids (only internal/github may); that logic now lives here and gitx calls it.
- `internal/effects`, `internal/committer`: exported `ErrTransient`; separate transient retry budget with jittered backoff, landed-push detection, same loop in the code and init lanes.
- `internal/closeout`, `internal/worker`, `internal/reaper/doc.go`: new `CausePushTransient` and its mapping to a retry plan.
- Tests in gitx, committer, closeout and worker for the cases in the plan.

## Testing
The previous run failed `TestOnlyEffectsWrites` (R1: `internal/gitx/write.go` imported go-github). After moving the check into `internal/github`, ran `go vet` and `go test` for gitx, github, effects (including archtest), committer, closeout and worker: all pass. The earlier full `go test ./...` run had no other failures.

## Open issues
- `go vet ./...` prints a parse error for `internal/roles/testdata/docs/skips/src/_x.go`; that testdata file is unrelated to this change.
- The GH006 protected-branch case is not covered, as the plan says; it needs a separate issue.
