---
role: engineer
status: done
attempt: 1
---
Adds the starter LLM scanner `scanners/architecture`: a prompt that checks code against ADRs and architecture docs and writes findings with stable `<rule>:<path>[:<symbol>]` fingerprints, plus its config override and `ard init` wiring.

## Changes
- `internal/roles/scanners/architecture/prompt.txt` (new): job, reference order, UNTRUSTED fencing, output schema, fingerprint rules, two worked examples.
- `internal/roles/config.yaml`: override (sonnet, every 3d, max_open 3, group_size 1, 20m, max_budget_usd 2); the loader accepts all keys.
- `internal/roles/roles.go`: embeds the role.
- `internal/initcmd/body.go`: PR body bullet with opt-out.
- Tests: `internal/roles/architecture_test.go` (new: config load, host-untrusted, prompt contract, examples pass `DecodeFindings` and `CheckLLMFingerprints`, drift cases fail), `roles_test.go` and `initcmd_test.go` updated.
- Docs: `docs/proposal.md`, `docs/layout.md`, `docs/site/roles/scanners.md` role lists.

## Testing
`go test ./internal/roles ./internal/initcmd ./internal/config` passes; gofmt and go vet clean on the changed packages. I did not run the full `make lint test`.

## Open issues
- The prompt ships to every `ard init` adopter and runs unattended in host mode (#84); a maintainer should read it before merge.
- Rolling the scanner into this repository's own `.ard/` (protected paths) is a human step.
