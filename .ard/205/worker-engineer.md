---
role: engineer
status: done
attempt: 1
---
Adds an optional `aid` executable to the reviewer role-directory contract. The reviewer loop runs it as its own read-only, credential-free, no-network executor job before the agent, validates its JSONL and passes it on as an untrusted input. Owner decisions are enforced in close-out: a gitleaks hit on an added line is injected as a blocking `high` finding, and a failed, timed-out or invalid aid can only return `changes`.

## Changes
- Config: `Role.AidPath`, discovery (reviewers only, must be executable, an error elsewhere), `ard config check` output, and `roles.ExecutableNames` gains `aid` (so init writes it 100755).
- `internal/reviewer/aid.go`, `run.go`, `loop.go`: the aid job (`NetworkNone`, no Env or Fixed, 5m timeout counted inside the review timeout, 256 KiB cap), `validateAid` (drops and counts bad lines, redacts and secret-scans messages, never keeps matched text), `aid.jsonl` or an "aid failed: why" note for the agent, and a new optional `Deps.Secrets` wired in `servecmd/reviewer.go`.
- `internal/closeout/review.go`, `prepare.go`: `ReviewContext` gains `HasAid`, `AidFailed` and `Injected` (all optional in JSON); an approve becomes `changes` and the injected finding is listed; the review body carries a note. Roles without an aid behave as before.
- Starter `reviewers/security-review`: `aid` (gitleaks with --redact, gosec with -nosec, semgrep with vendored rules), `normalize.jq`, `rules/security.yml`, a `Dockerfile` on the ard-agent image, and prompt changes (read `aid.jsonl` first; run tools only when it is absent).
- `docs/proposal.md`: reviewer loop step 3, the security-review paragraph and table row, the role-directory tree, init note and the host-mode residual risk. `docs/TODO.md`: removed the #205 line.

## Testing
- `make lint` passes (the gofmt message about `internal/roles/testdata/docs/skips/src/_x.go` is a pre-existing deliberately broken fixture).
- `go test ./...` passes.
- New tests: config discovery and render, closeout aid rules and JSON round trip, fake-executor tests (output reaches the agent, job shape, invalid lines, 256 KiB cap, redaction, nonzero exit, timeout, prepare error, no aid, gitleaks blocking) and validator edge cases.
- Updated tests: embedded file lists, `ExecutableNames`, the security-review role shape (now has a Dockerfile, so it is no longer host-untrusted), and the prompt (size now under the 16 KiB cap).
- `integration` tag test `TestIntegrationSecurityReviewAid` (shell injection, a run-time-built secret, disabled TLS verification fixtures) compiles and vets, but was not run: it needs Docker, network and `ARD_AGENT_IMAGE`. The #43 S1, S4 and S5 fixtures are not in the repo, so these stand in for them.

## Open issues
- Verification pending human: the Dockerfile was written without network access. The gosec, gitleaks and semgrep versions are unverified guesses and are not checksum-pinned (gosec and gitleaks build from the Go proxy; semgrep installs from PyPI without hashes). Build the image and run the integration test before merge.
- The aid script, normalize.jq and the semgrep rules are tested only against sample reports, not against the real tools.
- The aid's host-mode run relies on the local executor's allow-listed environment, which I checked in `internal/executor/local/env.go`: no credential reaches it.
- Copying the starter aid into this repository's own `.ard/reviewers/` is a protected-path change left to a human.
- Untracked `.ard-input/205/` files are the controller's inputs, not part of the change.
