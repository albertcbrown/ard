---
role: engineer
status: done
attempt: 1
---
Adds an `ARD_AGENT_IMAGE` host setting: the local executor runs untrusted-input roles that have no Dockerfile in that pinned image under Docker, never falls back to the host, and the host warning no longer fires for covered roles. Startup and `ard config check` reject an invalid ref.

## Changes
- `internal/executor`: `ErrImageRef` and `CheckImageRef` (digest or local image ID only).
- `internal/executor/local`: `Options.AgentImage`, `ensurePulled`, a shared Docker setup for the default image, and updated host-warning wording.
- `internal/config`: `HostUntrustedWith`.
- `internal/servecmd`, `internal/configcmd`: read and validate the variable, probe Docker at serve startup, skip warnings for covered roles, log `serve.agent_image`.
- `docs/proposal.md`: documents the setting, precedence and no-fallback rule.
- Tests for all of the above.
- Review round: `internal/configcmd/config_test.go` now clears `ARD_AGENT_IMAGE` in the two tests that expect the host warning, so they hold on a host that sets it.

## Testing
Ran `go test` on `./internal/executor/...`, `./internal/servecmd`, `./internal/configcmd` and `./internal/config`; all pass.

## Review findings
- `internal/servecmd/serve.go:214` (blocking): not changed. `TestServeAgentImageValidation` in `serve_test.go` already covers an invalid ref (`ErrImageRef`, probe not run), Docker down ("Docker is not available") and a valid ref logging `serve.agent_image`. The Docker probe is already injectable through `dockerProbe`.
- `internal/servecmd/serve.go:219` (low): not changed. Both use `docker` today, so there is nothing to share yet.
- `internal/executor/local/hostwarn.go:15` (medium): partly fixed. `TestHostTextMentionsAgentImage` in `hostwarn_test.go` already covers the `hostHint` and `HostRoleText` wording. The unset-variable warning is checked by `TestCheckValid` and `TestCheckWarnsHostUntrustedAtErrorLevel` in `internal/configcmd/config_test.go`, now hermetic.

## Open issues
- `make lint` fails on a fixture under `internal/roles/testdata` that this change does not touch.
- The Docker integration test and the live Mac check are for a human.
