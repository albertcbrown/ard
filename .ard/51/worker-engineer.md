---
role: engineer
status: done
attempt: 1
---
Adds the deterministic `docs` starter scanner (PR A of the plan): a `scan` script plus a stdlib-only Go program that reports undocumented exported Go identifiers and missing package comments. It fails closed and autocloses. The LLM `docs-drift` part (PR B) is out of scope, as the plan says.

## Changes
- `internal/roles/scanners/docs/`: `docscan.go` (parse-only, `//go:build ignore`), `scan` (mode 755, modelled on cve's), `Dockerfile` (same pinned base image, `GOTOOLCHAIN=local`, `TMPDIR=/var/tmp`).
- `internal/roles/config.yaml`: `scanners/docs` override (every 3d, max_open 3, group_size 10, autoclose). `roles.go`: doc comment.
- `internal/roles/docs_test.go`, `docs_docker_integration_test.go` (integration tag), `testdata/docs/<case>/` fixtures with golden `want.jsonl`.
- `roles_test.go`: FS contents, executable check over both `scan` files, `TestEmbeddedDocsRoleLoadsAsScanner`. `initcmd_test.go`: starter tree, 100755 mode, docs override, `ard:scanner:docs` label.
- `docs/proposal.md`: Scanners table (docs, docs-drift rows), "The docs scanner" paragraph, Configuration. `CLAUDE.md`: layout line lists `scanners/docs` (protected path, so merge needs human approval).

## Testing
`make lint` and `make test` pass. New tests cover goldens for every planned case, fail-closed cases (syntax error, bad exclude glob, unreadable file, control character), symlinks, determinism, the `scan` script (golden output, exclude file, syntax error, unwritable HOME) and a run on this repo, plus the `DecodeFindings` round trip.

## Open issues
- `go test -tags integration -run Docs ./internal/roles/` builds the image but the container run failed here with "No space left on device" in `/var/tmp` (the local Docker disk is full), so the Docker integration test is unverified.
- Human follow-ups from the plan are untouched: `.ard/scanners/docs/`, `.ard/config.yaml`, `deploy/` and the label sync.
- The first run on ard's own tree will file about 28 low issues, bounded by `max_open: 3`.
