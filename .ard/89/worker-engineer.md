---
role: engineer
status: done
attempt: 1
---
Adds an optional, fail-closed gitleaks pass to the close-out secret scan, switched on by a new `redact.gitleaks` config block. A hit takes the existing flagged path (log stays host-local, `ard:needs-human`).

## Changes
- `internal/redact/gitleaks.go`: `Gitleaks(opts)` Scanner. It runs `detect --no-git --report-format json` over the redacted bytes in a private temp dir, with a scrubbed environment and a 2m timeout. Exit 0 needs an empty report, exit 2 needs findings, and any other exit, a missing binary, a timeout or a bad report is an error. Findings keep rule, line and column only, and stdout/stderr are discarded.
- `internal/redact/multi.go`: `Multi(...)` runs scanners in order, so the builtin pass stays.
- `internal/closeout/paths.go`, `internal/servecmd/reaping.go`: `JobRedactor` takes a gitleaks option func, which is read per job so config changes apply without a restart.
- `internal/config`: new `redact.gitleaks.enabled` (default false) and `redact.gitleaks.path` (default `gitleaks`), with parsing, rendering and the golden file.
- `docs/proposal.md`: Guardrails and the "All agent output is committed too" paragraph.
- Tests: fake-binary unit tests (hit, clean, missing, error exit, bad report, timeout, argv/input, Multi, Redactor level), closeout tests (off, disabled, hit, missing binary), config tests, and `TestGitleaksReal`.

## Testing
`go test ./...` and `make lint` pass. `TestGitleaksReal` skips unless `ARD_GITLEAKS_BIN` is set, so it did not run here.

## Open issues
- `PinnedGitleaksVersion` (8.28.0) is my choice. A maintainer should confirm or change it, and run the integration test with a real binary.
- Shipping the binary in the image needs Dockerfile or deploy/ changes, which are protected paths and are not touched.
- `.ard/config.yaml` is not edited, so the pass stays off until a maintainer enables it.
- A maintainer should confirm that #20 is complete.
