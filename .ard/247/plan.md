# Plan for #247

## Implementation

### Steps

1. `internal/cli/cmd_version.go` and `cmd/ard/main.go`: make the version resolve in one place. In `cmd/ard/main.go` (new) add `func resolveVersion(v string, info *debug.BuildInfo, ok bool) string`: when `v != "dev"` return it; else when `ok` and `info.Main.Version` is non-empty and not `(devel)` return it; else return `"dev"`. `run` passes `resolveVersion(version, readBuildInfo())` to `cli.Run`, where `readBuildInfo` is a thin wrapper over `debug.ReadBuildInfo`. The `-X main.version` value still wins, so `make build` is unchanged. Reason: `go install ...@vX.Y.Z` carries the tag only in build info. Pseudo-versions (`v0.0.0-...`) are returned as is; they are honest.
2. `Makefile`: change line 1 to `VERSION ?= $(shell git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude 'v*-*' --always --dirty 2>/dev/null || echo dev)`. Keep `LDFLAGS` and `-X main.version`. Reason: a tag like `v1.2.3-rc1` or a non-version tag must not name a build.
3. New `internal/versioncheck/doc.go` (package comment modelled on `internal/botcheck/doc.go`: tests only, scripts extracted from `.github/workflows/version.yml` steps `plan` and `tag`).
4. New `internal/versioncheck/versioncheck_test.go` (package `versioncheck_test`), helpers modelled on `internal/botcheck/botcheck_test.go` (`loadYAML`, `as`, `git`, `plant`):
   - `script(t, job, stepID)` reads `../../.github/workflows/version.yml`, returns `jobs.<job>.steps[id=<stepID>].run`; fail if it contains `${{`.
   - `fixture(t)` builds a bare "origin" repo plus a working clone with `origin/main`, full history and an annotated seed tag `v0.1.0` (clone not shallow), using `GIT_CONFIG_GLOBAL=/dev/null`. Helpers to add a squash commit (`title (#N)`), a merge commit (`Merge pull request #N from x/y`, `--no-ff`) and a direct push, with a settable committer identity.
   - `fakeGH(t, dir, state)` writes an executable `gh` shell script on a prefixed `PATH`. It serves `gh api repos/R/commits/SHA/pulls`, `repos/R/pulls/N` and `repos/R/releases/tags/T` from JSON files in a state dir, answers 404 as `HTTP 404` on stderr with exit 1, and logs `gh release create ...` argv to a file. Real `jq` and `base64` are needed; `t.Skip` if `jq` is missing, since CI has it.
   - `runPlan(t, clone, env)` runs `bash -c <script>` in the clone with `REPO`, `BRANCH=main`, `BOT_LOGIN`, `ENABLED=on`, `EVENT=push`, `RETRY_SLEEP=0`, `GITHUB_OUTPUT` and `GITHUB_STEP_SUMMARY` set to temp files, and returns exit code, stdout and the decoded plan rows.
   - `runTag(t, clone, plan, env)` runs the `tag` script with `PLAN_B64`, `LIVE=true`, `APP_SLUG`, `BOT_LOGIN`, `TAGGER_NAME`, `TAGGER_EMAIL`, `PUSH_TOKEN` empty, the fake `gh`, and `origin` pointing at the bare repo.
5. Test cases in `versioncheck_test.go` (see Test plan). Run the real scripts only; do not copy them.
6. Docs: `docs/layout.md`, add `internal/versioncheck/   tests for the version workflow in .github/workflows/version.yml (plan and tag scripts)` under the `internal/botcheck/` line. Do NOT edit `CLAUDE.md`: its Layout section now points to `docs/layout.md` and says a PR updates that file, not CLAUDE.md. `CLAUDE.md` is also a protected path.
7. Docs: `docs/proposal.md` Delivery: add a **Versioning** bullet (every first-parent merge to `main` gets `vX.Y.Z`, bump from `semver:major` / `semver:minor` labels, else patch; ard's main-lane commits skipped; seed tag `v0.1.0`; `version.yml` dry run until `ARD_VERSIONING` is `on`; `make build` and `go install ...@vX.Y.Z` print the tag) and amend the Release bullet. In Guardrails add that the tag-writing credential lives in the `release` environment, the release App must differ from ard's App, tags are never moved or deleted, and `.github/` plus the version tests protect the logic.
8. New `docs/runbooks/release.md`: seed-tag creation (`git tag -a v0.1.0 <sha>` and push), settings checklist (variables `ARD_BOT_LOGIN`, `ARD_VERSIONING`, `RELEASE_APP_ID`, `RELEASE_BOT_NAME`, `RELEASE_BOT_EMAIL`, secret `RELEASE_APP_PRIVATE_KEY`, `release` environment), dry-run to live switch, `workflow_dispatch` with `max_tags`, the `/vN` module-path guard (a `semver:major` to v2 needs go.mod `/v2` first), and recovery (rerun; backfill; never delete tags). Take the names from `version.yml`.
9. If a test exposes a bug in `version.yml`, do not edit `.github/`. Keep the failing case as `t.Skip("bug: ...")` with the reason and describe the bug in the PR so a human fixes the workflow.

### Test plan

Command: `make lint test` (also `go test ./internal/versioncheck ./cmd/ard ./internal/cli`). Files: `internal/versioncheck/versioncheck_test.go`, `cmd/ard/main_test.go`.

- plan, squash commit `t (#5)` with an API-returned PR labelled none: one `tag` row `v0.1.1`, bump `patch`, PR 5.
- plan, merge commit `Merge pull request #6 from a/b`: row `v0.1.1`, PR 6.
- plan, direct push with no PR (API returns `[]`, subject has no number): row `v0.1.1`, empty PR, warning "no merged PR".
- labels: `semver:minor` gives `v0.2.0`; `semver:major` gives `v1.0.0` (needs go.mod without `/vN`); both labels gives major plus a warning; an unrelated label gives patch.
- ard commits skipped: committer name equal to `BOT_LOGIN`, committer email `BOT_LOGIN@users.noreply.github.com`, numeric-id `+login@users.noreply` form, mixed case, and a control character in the identity; none produce a row. A human commit after them does, numbered from the base.
- two quick merges: two untagged commits produce `v0.1.1` then `v0.1.2` oldest first; with a minor label on the first, `v0.2.0` then `v0.2.1`.
- reruns: after running `tag` for the first plan and fetching tags, a second `plan` gives an empty plan and `count=0`; a replan after a failure on the second row resumes at that row; rerunning `tag` with the same plan fails with "already has version tag" or "already exists" and moves no tag.
- association lag: `commits/SHA/pulls` empty first, then filled; plan retries (`RETRY_MAX=3`, `RETRY_SLEEP=0`) and uses the PR; with the number only in the subject it falls back to `pulls/N`.
- v2 guard: `semver:major` while at `v1.x.y` with go.mod `module .../ard` exits 2 with "needs a module path ending in /v2"; with `/v2` it passes; a `/v2` module while the tag stays below v2 exits 2.
- backfill: seed tag with release API 404 gives a `backfill` row; `tag` creates only a release (fake gh log).
- no tag yet: dry run exits 0 with empty plan; live exits 2.
- dry run: `ENABLED` unset gives `live=false`; `workflow_dispatch` with `INPUT_DRY_RUN=true` gives `live=false`; `false` gives live.
- cap: `MAX_TAGS=1` with two commits exits 2.
- API error (non-404 on `gh`): exits 2 and no plan.
- tag script: creates annotated tags (`git cat-file -t` is `tag`), tagger is `TAGGER_NAME`, pushes to the bare origin, calls `gh release create` with `--verify-tag`; refuses when `LIVE` is not `true`, when `APP_SLUG[bot]` equals `BOT_LOGIN`, when tagger vars are empty, on a malformed row, and when the sha is not first-parent.
- title with backticks, newlines and `%` is neutralised in the notes and in `::error` output.
- bot-commit audit stays green: `go test ./internal/botcheck` is unchanged and passes; add a check that the version workflow's skip rule agrees with `bot-commits.yml` for the identity forms above (parity with `botcheck`).
- `TestWorkflowShape`: `version.yml` has no `${{` in the two `run:` blocks and `plan` has `contents: read` only.
- `resolveVersion` in `cmd/ard/main_test.go`: `("v1.2.3", any)` gives `v1.2.3`; `("dev", Main.Version "v1.4.0")` gives `v1.4.0`; `("dev", "(devel)")` gives `dev`; `("dev", ok=false)` gives `dev`; existing `ard dev` test still passes.
- Manual check, not in `make test`: on a tagged commit `make build && bin/ard version` prints `ard vX.Y.Z`; a tag `v1.2.3-rc1` and a lightweight non-version tag are ignored; `go install` of a tag prints the tag after release.

### Risk

Low to medium. No protected path is touched by this plan (`CLAUDE.md` and `.github/` are left alone). The tests only read `version.yml`. The `ard version` change touches a public command's output, and only when no ldflag is set. The tests shell out to `bash`, `git`, `jq` and a fake `gh`, so they depend on bash 4+ features (`declare -A`) and on `jq`; macOS system bash 3.2 would fail, so the tests must find a bash 4+ on `PATH` and skip otherwise. Part B's workflow is already on main. A workflow bug found by the tests needs a human fix in `.github/`.

### Size

L, about 900 changed lines, under the 3,000-line block limit and the 2,000-line warning (neither is set in `.ard/config.yaml`). Files: `internal/versioncheck/doc.go` (new), `internal/versioncheck/versioncheck_test.go` (new), `cmd/ard/main.go`, `cmd/ard/main_test.go`, `Makefile`, `docs/layout.md`, `docs/proposal.md`, `docs/runbooks/release.md` (new).
