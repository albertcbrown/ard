---
role: engineer
status: done
attempt: 1
---
Adds tests that run the version workflow's `plan` and `tag` scripts against bare-repo fixtures and a fake `gh`. Also resolves `ard version` from build info for `go install ...@vX.Y.Z`, limits the Makefile's `git describe` to plain version tags, and updates the docs.

## Changes
- `internal/versioncheck/` (new): `doc.go` and `versioncheck_test.go`. Scripts are extracted from `version.yml` and never copied.
- `cmd/ard/main.go`: `resolveVersion` prefers `-X main.version`, then `debug.ReadBuildInfo` (not empty, not `(devel)`), else `dev`. `main_test.go` covers it.
- `Makefile`: `git describe` matches only `vN.N.N` and excludes `v*-*`.
- `docs/layout.md`: the `internal/versioncheck/` line. `CLAUDE.md` is untouched, since it points to `docs/layout.md`.
- `docs/proposal.md`: a Versioning bullet in Delivery, an amended Release bullet and a Guardrails bullet.
- `docs/runbooks/release.md` (new): seed tag, settings checklist, dry run to live, module-path guard, recovery.

## Testing
Ran `go test ./internal/versioncheck ./cmd/ard ./internal/cli ./internal/botcheck`; all pass. The tests cover squash, merge-commit and direct-push commits; each label case and both labels; ard identity forms skipped; two quick merges; reruns and resume after a release failure; association lag and the `pulls/N` fallback; the v2 module guard; backfill; no tag, dry-run modes, the cap and an API error; tag refusals; title neutralising; and the workflow shape. They need bash 4+ and jq and skip without them. The tests found no workflow bug.

## Open issues
- `make lint` stops at `go vet` on `internal/roles/testdata/docs/skips/src/_x.go` ("expected ')'"). That file is not part of this change. I did not run the full `make test`, which timed out in my sandbox.
- Not done: the parity check against `bot-commits.yml` identity rules. The skip rule is tested directly, but not compared with the audit.
- Not done: the manual `make build` and `go install` check on a tagged commit.
