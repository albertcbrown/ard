# Plan for #47

## Implementation

The design and its nine decisions are settled in the issue's "Implementation plan" and "Resolved open questions". The steps below follow that design and add what the code check found. Differences from the issue text are marked **Correction**.

**Corrections the worker must apply**
- **go:embed needs an edit.** `internal/roles/roles.go:14` lists directories explicitly (`scanners/cve scanners/docs scanners/error-codes`). It is not a `scanners` glob. Add `scanners/lts` to the directive, or the role will not be embedded.
- **Alpine slug is `alpine-linux`.** `products.json` maps image `alpine` to `alpine-linux` (see `internal/roles/testdata/lts/CAPTURE.md`). Every test that mentions Alpine uses `alpine-linux`.
- **Fixtures.** `internal/roles/testdata/lts/api/` already holds real captures of go, nodejs, python, debian, ubuntu, alpine-linux, redis, nginx and angular. Do not regenerate or edit them. The worker has no network: it cannot run the integration-tagged tests or `make image-test`.
- **yq pin.** v4.54.3. SHA-256 amd64 `129534601871538efe0ba4c5e62a34fb1d5f469bcb975c4fddeb467d82974973`, arm64 `89f69310b61b68d1245065a1241d1fc843f7a36cd6103ffd77ad987613bd1b67`, from `testdata/lts/CAPTURE.md`. Download `https://github.com/mikefarah/yq/releases/download/v4.54.3/yq_linux_<arch>`. Do not invent any other value.
- Before writing `eol.jq`, read the committed `api/go.json` to confirm the v1 field names (`result.name`, `label`, `releases[].name`, `codename`, `isEol`, `eolFrom`, `isLts`, `latest.name`). Adapt to what is really there.

### Steps

Order keeps `go build ./...` and `go test ./...` passing after each step (new tests are added together with the files they need).

1. **New `internal/roles/scanners/lts/products.json`.** Shape `{"images":{…},"npm":{…}}`. images: golang→go, node→nodejs, python→python, alpine→alpine-linux, debian→debian, ubuntu→ubuntu, postgres→postgresql, redis→redis, nginx→nginx, mysql→mysql, mariadb→mariadb, ruby→ruby, php→php, eclipse-temurin→eclipse-temurin. npm: @angular/core→angular, vue→vue, next→nextjs, nuxt→nuxt, electron→electron. All 19 slugs were verified to answer 200.
2. **New `internal/roles/scanners/lts/refs.jq`.** Pure jq, valid on jq 1.6 (no `pick`, `abs`, `trim` or other 1.7+ builtins). Input is the `inputs.jsonl` records (`kind` gomod, packagejson, dockerfile or helmvalues). Output is `{"product","version","path","line","ref","floor"}` records, plus `{"skip":"…"}` lines, sorted by product, version, path, line. Implement decision 8 (go.mod toolchain over `go` directive; `engines.node` and npm ranges with lowest-lower-bound parsing; Dockerfile `FROM` with global ARG substitution, registry normalisation, digest dropping, tag to version or codename; Helm `repository`+`tag` and `image: name:tag`). A `package.json` that does not `fromjson` calls `error("lts: cannot parse <path>")`.
3. **New `internal/roles/scanners/lts/eol.jq`.** Pure jq. Args `--arg today`, `--slurpfile refs`, `--slurpfile api`. Match cycles by decision 4 (codename first word, then longest dotted prefix). Group by product and cycle. Severity per decision 5 and the floor cap (decision 9): high if `eolFrom` ≤ today or (`isEol` and null `eolFrom`); medium within 90 days; low within 180; else nothing. Strip control characters and backticks from each ref, cap at 200 characters, render in an inline code span; list at most 50 refs then `…and N more`. Emit one compact line per finding with keys in order `fingerprint` (`eol:<product>:<cycle>`), `title` (`<label> <cycle>: end of life <eolFrom>` or `… (no date)`), `severity`, `group` (the product slug), `files` (sorted, unique), `body`. Sort by fingerprint. Unmatched versions give `{"skip":…}`. Error if a referenced product has no API entry or no `releases` array.
4. **New `internal/roles/scanners/lts/scan`, mode 0755, POSIX sh, `set -eu`.** Copy the header contract and helper style (`need`, `die`, temp dir with trap, stdout last) from `internal/roles/scanners/cve/scan`. Follow the issue's eight steps: tool check (`yq` only if a `Chart.yaml` exists, exit 2 when missing); `find` with prune of `.git`, `vendor`, `node_modules`, `testdata`, no `-L`, `LC_ALL=C sort`; build `inputs.jsonl`; run `refs.jq`; one curl per distinct product (slug check `^[a-z0-9][a-z0-9.-]{0,63}$`; `--proto =https --tlsv1.2 -sS --max-time 30 --retry 2 --max-filesize 10M -A ard-lts-scanner`; require HTTP 200); `LTS_TODAY` override with format check; merge API files with `jq -s` into `$tmp/api.json`; run `eol.jq`; skip lines go to stderr, findings to stdout last. No GNU-only flags. Exit codes 0, 1, 2 as cve. Any data gap fails with empty stdout.
5. **New `internal/roles/scanners/lts/Dockerfile`.** Same header style and same `GO_IMAGE` digest as `scanners/cve/Dockerfile`. Add jq 1.8.2 with cve's checksums, and yq v4.54.3 with the two checksums above. Do not copy `scan`.
6. **Edit `internal/roles/roles.go`.** Add `scanners/lts` to the `//go:embed` line and to the doc comment's role list. `ExecutableNames` is unchanged.
7. **Edit `internal/roles/config.yaml`.** Add `scanners/lts: { every: 7d, max_open: 5, group_size: 1, autoclose: true }` after the cve line (line 30).
8. **Tests, `internal/roles/lts_test.go` (new).** See Test plan. Fixtures under `internal/roles/testdata/lts/refs/`, `eol/` and the hand-written `api/synthetic.json` (new, never regenerated). Reuse `needTools`, `decode` and `readFixture` from `cve_test.go`.
9. **Edit `internal/roles/roles_test.go`.** `TestFSContents` (line 32) gains the five lts paths (`Dockerfile`, `eol.jq`, `products.json`, `refs.jq`, `scan`) in sorted position. Make `TestScanIsExecutableOnDisk` (line 46) cover lts. Add `TestEmbeddedLtsRoleLoadsAsScanner` and `TestLtsProductsJSON` modelled on the cve test near line 77.
10. **Edit `internal/initcmd/initcmd_test.go`.** `starterTree` (line 146) gains `.ard/scanners/lts/{Dockerfile,eol.jq,products.json,refs.jq,scan}` in sorted order. Check the assertion near line 201 and any count that depends on the starter list; fix what breaks.
11. **New `internal/roles/testdata/lts/regen.sh`** (fetches the nine products with `jq .` and regenerates eol `want.jsonl`, like `testdata/cve/regen.sh`). **Edit `Makefile`**: add `lts-fixtures` and add it to `.PHONY`. The worker cannot run it.
12. **Integration tests (new, `//go:build integration`)**: `lts_integration_test.go` and `lts_docker_integration_test.go`, as in the issue. Compile-check with `go vet -tags integration ./internal/roles/`. Do not run them.
13. **Root `Dockerfile`**: add yq v4.54.3 in the `tools` stage (mirror the jq lines near 54 to 65 with its SHA values) and the `COPY`; update the header comment. **`deploy/test-image.sh`**: `tools_ok` checks yq; add the lts host-subprocess case. **`deploy/charts/ard-serve/templates/networkpolicy.yaml`**: comment only. All are merge-protected, so the PR waits for a human.
14. **Docs.** `docs/proposal.md`: lts row of the Scanners table, a "The lts scanner" paragraph after the cve paragraph, and the `ard init` starter list. `CLAUDE.md` Layout: both `internal/roles/` lines list `scanners/lts`.
15. **Do not** touch `.ard/config.yaml` or `.ard/scanners/` (adding lts to ard's own `.ard/` is a separate human PR).

### Test plan

Command: `make lint test` (repo's target; also `go test ./internal/roles/ ./internal/initcmd/`). Tests needing `jq` skip through `needTools`. Helm cases also need `yq`.

- `TestLtsRefsGolden` (`testdata/lts/refs/<case>/{input.jsonl,want.jsonl}`): go directive only (floor); toolchain wins; `engines.node` `>=18`, `^18 || ^20`, `>=16.0.0 <21` (→ 16) and `*` skipped; npm dep in and not in the table; the Dockerfile matrix (ARG substitution, `--platform`, earlier stage, `scratch`, docker.io/library digest, lowercase `from`, `latest` skipped, unset ARG skipped, unmapped registry); Helm (`repository`+`tag`, `image: "nginx:1.25"`, numeric tag, unmapped); unparseable `package.json` → jq non-zero; hostile text (backticks, mention, `<!--`, newline).
- `TestLtsEolGolden` (`testdata/lts/eol/<case>/{refs.jsonl,today,want.jsonl}` against `api/`): past EOL → high; 30 days → medium; 150 days → low; 400 days → none; `isEol` with null `eolFrom` → high (synthetic); null `eolFrom` and not EOL → none; floor-only capped at medium, floor plus Dockerfile uncapped; two files one cycle → one finding; two cycles one product → two findings, same group; codename `bookworm` and `jammy`; no cycle → skip line; missing API product → error; more than 50 refs → truncated; hostile ref inert; exact boundaries at 90 and 180 days (synthetic). Expected values depend on each case's fixed `today` and the committed capture, never a live fetch.
- `TestLtsScanHermetic`: repo written from Go literals into `t.TempDir()`, fake `curl` on PATH serving `testdata/lts/api/<p>.json` with `200`, `404`, or exit 7. Cases: empty repo → exit 0, no stdout; mixed repo → schema-valid findings; manifests under `testdata/` and `node_modules/` ignored; mapped product 404 → exit 1, empty stdout; curl exit 7 → exit 1, empty stdout; bad `package.json` → exit 1, empty stdout; `Chart.yaml` with `yq` missing → exit 2; bad `LTS_TODAY` → exit 1.
- `TestLtsFixtureCasesListed`: every fixture directory is in a case list and the reverse.
- `roles_test.go`: `TestFSContents`, `TestScanIsExecutableOnDisk`, `TestEmbeddedLtsRoleLoadsAsScanner` (`Every == 7*24h`, `MaxOpen == 5`, `GroupSize == 1`, `Autoclose`, `Extra == [eol.jq products.json refs.jq]`), `TestLtsProductsJSON` (two top-level keys, slug regex, no duplicate keys).
- `initcmd_test.go`: the starter tree test covers the five lts paths and the executable bit on `scan`.
- Not run by the worker (maintainer runs by hand): `go test -tags integration ./internal/roles/ -run Lts` (including `TestIntegrationLtsProductsExist`) and `make image-test`.

### Risk

**Medium.**
- Wrongful autoclose from partial data: mitigated by failing closed with empty stdout, and tests assert this on each failure path.
- Merge-protected paths are touched (root `Dockerfile`, `deploy/`, `CLAUDE.md`), so a human must approve the PR.
- Supply chain: a new pinned binary (yq) is fetched in two Dockerfiles. Checksums come only from the capture record.
- Security-sensitive text handling: untrusted manifest text reaches an issue body, so it must be neutralised in `eol.jq`. Untrusted text never reaches a URL.
- The jq 1.6 compatibility of CI's jq is an easy thing to get wrong.
- The new role is embedded and starts in `ard init`. Sibling scanner PRs edit the same lists and need a trivial rebase. No migrations. No concurrency beyond the scan script.

### Size

**L** (about 400 lines of sh and jq plus about 500 lines of Go tests and fixtures; well under the 2,000-line limit, but above the 1,000-line warning if fixtures are counted, so expect a large-change note).

Files: new `internal/roles/scanners/lts/{scan,refs.jq,eol.jq,products.json,Dockerfile}`; new `internal/roles/lts_test.go`, `lts_integration_test.go`, `lts_docker_integration_test.go`; new `internal/roles/testdata/lts/{regen.sh,refs/**,eol/**,api/synthetic.json}`; edited `internal/roles/{roles.go,config.yaml,roles_test.go}`, `internal/initcmd/initcmd_test.go`, `Makefile`, `Dockerfile`, `deploy/test-image.sh`, `deploy/charts/ard-serve/templates/networkpolicy.yaml`, `docs/proposal.md`, `CLAUDE.md`.
