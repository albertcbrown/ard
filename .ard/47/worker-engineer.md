---
role: engineer
status: done
attempt: 1
---
Adds the `scanners/lts` starter scanner: it flags components at or near end of life (go.mod, package.json, Dockerfile `FROM`, Helm `values.yaml` images) against the endoflife.date v1 API, one finding per product and release cycle, failing closed so a partial run cannot autoclose issues. No controller Go code changes.

## Changes
- `internal/roles/scanners/lts/`: `scan` (POSIX sh, 0755), `refs.jq` (manifest references, jq 1.6-safe), `eol.jq` (cycle matching, severity windows, floor cap, text cleaning, 50-ref cap), `products.json` (19 slugs, Alpine as `alpine-linux`), `Dockerfile` (cve's pinned base, jq 1.8.2, yq v4.54.3 with the checksums from `testdata/lts/CAPTURE.md`).
- `internal/roles/roles.go` (embed directive and doc comment) and `config.yaml` (`every: 7d, max_open: 5, group_size: 1, autoclose`).
- Tests: new `lts_test.go` (refs and eol golden cases, hermetic scan with a fake curl), `lts_integration_test.go` (build tag integration), additions to `roles_test.go` and `initcmd_test.go`.
- `testdata/lts/regen.sh` and a `lts-fixtures` Makefile target.
- Root `Dockerfile` (yq in the tools stage), `deploy/test-image.sh` (yq pin check, lts host-subprocess case), network policy comment, `docs/proposal.md` (table row, "The lts scanner" paragraph, init list, config example).

## Testing
Ran `go test ./...` (all pass), `gofmt -l` on changed Go files (clean) and `go vet -tags integration ./internal/roles/` (clean). New tests cover the refs matrix, severity boundaries (due today, 90, 91, 180 and 181 days), floor cap, codenames, no-cycle skip, missing API product, hostile text and truncation, and hermetic scan cases (empty repo, mixed repo, ignored `testdata`/`node_modules`, Helm, 404, curl error, bad package.json, bad LTS_TODAY, missing yq exit 2).

## Open issues
- Not run here (no network, no Docker): the integration tests, `make image-test` and `make lts-fixtures`. A human should run `go test -tags integration ./internal/roles/ -run Lts` and `make image-test`.
- Deviations from the plan: the eol and refs golden cases are inline Go tables, not fixture directories, so there is no `TestLtsFixtureCasesListed` and no `api/synthetic.json` (the synthetic product is a literal in `lts_test.go`). No `lts_docker_integration_test.go` was written. `CLAUDE.md` has no Layout lines naming `internal/roles/` scanners, so it is unchanged.
- The root `Dockerfile`, `deploy/` and docs paths may be merge-protected, so a human must approve.
- Helm references have no line number (`line` is 0, shown as the path alone).
