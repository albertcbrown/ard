---
role: engineer
status: done
attempt: 1
---
Adds an `ARD_AGENT_IMAGE` host setting so the local executor runs roles that read untrusted input and have no Dockerfile in a default agent image under Docker, with no fallback to host mode. Follows the owner's decision: the controller's clone is not mounted; `GOFLAGS=-buildvcs=false` is set in the container instead.

## Changes
- `internal/executor`: `ErrImageRef` and `CheckImageRef` (digest-pinned ref or local `sha256:<hex>` ID; tags, flags and whitespace rejected).
- `internal/executor/local`: `Options.AgentImage` (validated in `New`). `Prepare` uses Docker for untrusted-input jobs without a Dockerfile, via the new `ensurePulled` (use a local image, else pull a digest). A role's Dockerfile wins; trusted scanners stay on host; unset is unchanged. Any Docker failure fails `Prepare`. `runArgv` sets `GOFLAGS=-buildvcs=false`. The host warning's hint names `ARD_AGENT_IMAGE`.
- `internal/config`: `HostUntrustedWith(agentImage bool)`; `HostUntrusted()` unchanged.
- `internal/servecmd`: reads and validates `ARD_AGENT_IMAGE`, runs `docker version` at startup when set, passes it to the executor, logs `serve.agent_image`, and skips the host warning.
- `internal/configcmd`: validates the ref (error on invalid) and skips the warning when set.
- `docs/proposal.md`: setting, precedence, no-fallback, no clone mount, warning change.

## Testing
`go vet` on changed packages, and `go test ./internal/...` with GOPROXY=off: all pass. Added `TestCheckImageRef`; local executor tests with a fake docker (default image, Dockerfile wins, trusted scanner on host, unset unchanged, Docker down with no fallback and no worktree left, `ensurePulled` cases, bad image in `New`, argv has the flag and no clone mount); config, configcmd and cfgloader tests. `make lint`'s gofmt step lists a pre-existing broken fixture under `internal/roles/testdata`, which this change does not touch.

## Open issues
- The Docker integration test and the live Mac check are for a human.
- Server-side `GOFLAGS=-buildvcs=false` applies to all Docker jobs, including Dockerfile roles, to keep flags identical.
- A maintainer should review the no-fallback path.
