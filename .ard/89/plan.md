# Plan for #89

## Implementation

### Steps
1. New `internal/redact/gitleaks.go`: `type GitleaksOptions struct { Path string; Timeout time.Duration }` and `func Gitleaks(o GitleaksOptions) Scanner` (new). `Scan(ctx, r)` copies `r` into a file in a fresh `os.MkdirTemp` dir (mode 0700, removed on return), then runs `exec.CommandContext(ctx, path, "detect", "--no-git", "--no-banner", "--redact", "--exit-code", "2", "--source", <file>, "--report-format", "json", "--report-path", <report>)` with a minimal environment (no secrets, `HOME` set to the temp dir) and a default timeout (new, 2m). Exit-code handling, all fail closed: 0 means parse the report, which must be empty or `[]`, and anything unparsable is an error; 2 means leaks, so parse the report into `[]Finding{Rule: RuleID, Line: StartLine, Col: StartColumn}` using only those fields (never `Match`/`Secret`), with `Rule` prefixed `gitleaks:`; any other exit, a missing binary (`exec.ErrNotFound` or a stat error), a timeout or a missing/invalid report returns an error with the stderr tail omitted from the message (stderr may echo content). Exit 2 with zero parsed findings is an error. Reason: `--exit-code 2` separates "leaks found" from gitleaks' own error exit (1).
2. New `internal/redact/multi.go`: `func Multi(scanners ...Scanner) Scanner` (new) runs each in order, concatenates findings, returns the first error with nil findings, and respects `ctx`. Reason: `Options.Scanner` takes one scanner, and the builtin pass must stay.
3. `internal/closeout/paths.go`: change `JobRedactor(environ, host)` to take a third argument `gitleaks func() (redact.GitleaksOptions, bool)` (nil or false means off). When on, build `redact.Options{Scanner: redact.Multi(redact.Builtin(), redact.Gitleaks(opts))}`. Update callers: `internal/servecmd/reaping.go:53` and `internal/closeout/paths_test.go:89`. A hit already follows the flagged path in `internal/closeout/prepare.go` (`res.Flagged` leads to host-local log, needs-human), and an error already fails closed ("redaction could not complete"), so no change is needed there.
4. `internal/config`: add a top-level `redact:` block with `redact.gitleaks.enabled` (bool, default false) and `redact.gitleaks.path` (string, default `gitleaks`; absolute path or a name on PATH). Touch `types.go` (new `Redact` struct on `Resolved`), `defaults.go`, `raw.go` (a `parseRedact` like `parseStale`, unknown keys rejected), `render.go` (new `redactYAML`). Do not edit `.ard/config.yaml` (protected); it stays off by default.
5. `internal/servecmd/reaping.go`: pass a closure reading the current resolved config (as `cfgRoles{w}` does) so enabling it takes effect on the next close-out without a restart.
6. `docs/proposal.md`, Guardrails: in the "All agent output is committed too" paragraph and the configuration section, document the optional second pass: the `redact.gitleaks` keys, the pinned binary, the `detect --no-git` invocation, fail closed on missing binary, error or timeout, and that a hit is treated like an internal hit (log stays host-local for 7 days, `ard:needs-human`).
7. Integration test (see below). Do not edit `Dockerfile`, `Jenkinsfile*` or `deploy/` (protected); pinning gitleaks into the image is a separate maintainer change.

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`.
Unit tests in new `internal/redact/gitleaks_test.go` use a fake binary: a shell script written to `t.TempDir()` (skip on Windows), selected by `GitleaksOptions.Path`:
- `TestGitleaksHit`: fake writes a one-finding JSON report to `--report-path` and exits 2. Expect one Finding with the rule, line and column; the finding holds no secret text.
- `TestGitleaksClean`: fake exits 0 and writes `[]`. Expect no findings and nil error.
- `TestGitleaksMissingBinary`: `Path` is a nonexistent file. Expect an error and nil findings.
- `TestGitleaksErrorExit`: fake exits 1 (and prints text to stderr). Expect an error, and the message must not contain the stderr text.
- `TestGitleaksBadReport`: exit 0 with garbage report, and exit 2 with empty report. Both expect errors.
- `TestGitleaksTimeout` and cancelled ctx: the fake sleeps. Expect an error.
- `TestGitleaksArgs`: the fake records its argv and the input file contents. Check `detect --no-git --report-format json` and that it received the redacted bytes; temp dir removed afterwards.
- `TestMulti`: builtin hit plus clean gitleaks yields the builtin finding; either scanner's error fails the whole scan.
- Redactor level, in `redact_test.go`: `Log` with `Multi(Builtin(), fake hit)` returns `Flagged`, nil `Clean` and no leftover `dst`. A fake error returns an error and no `dst` or temp file.
- `internal/closeout/paths_test.go`: `JobRedactor` with gitleaks off uses the builtin only; on, with a fake hit, the log is not committed and the needs-human path is taken (extend the existing prepare tests).
- `internal/config`: `redact.gitleaks` parse, default off, unknown key rejected, render round trip, non-bool `enabled` rejected.
Integration: `TestGitleaksReal` in `internal/redact/gitleaks_integration_test.go`. It reads the binary path from env `ARD_GITLEAKS_BIN` and calls `t.Skip` when unset, so `make test` stays offline. It asserts that a planted fake AWS-style key line is flagged and a plain log is clean. Also check `gitleaks version` against a pinned constant (new, e.g. `PinnedGitleaksVersion`) so a drifted binary fails.

### Risk
Medium. It runs an external binary over agent-influenced text, so use argv with no shell, a scrubbed environment and a private temp dir. Security-sensitive: fail-closed paths must be tested. `JobRedactor`'s signature changes (internal, two callers). A new config key is a public schema addition but is optional and off by default. No migrations or concurrency changes (each scan uses its own temp dir). Protected paths from `merge.protected_paths` are not touched by this plan; shipping the pinned binary does need `Dockerfile` or `deploy/` changes and needs a human.

### Size
M (about 250 to 300 changed lines including tests and docs). Files: new `internal/redact/gitleaks.go`, `internal/redact/multi.go`, `internal/redact/gitleaks_test.go`, `internal/redact/gitleaks_integration_test.go`; edited `internal/redact/redact_test.go`, `internal/closeout/paths.go`, `internal/closeout/paths_test.go`, `internal/servecmd/reaping.go`, `internal/config/{types,defaults,raw,render}.go` plus config tests, `docs/proposal.md`.
