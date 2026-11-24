# Release runbook

`.github/workflows/version.yml` tags every first-parent merge to `main` as `vX.Y.Z` and creates a notes-only release. This runbook covers setup and recovery. Names come from the workflow.

## Seed tag

An admin creates the annotated seed tag once, on a commit on `main`:

```
git tag -a v0.1.0 <sha> -m v0.1.0
git push origin refs/tags/v0.1.0
```

Until it exists a dry run plans nothing and a live run fails.

## Settings checklist

- Repository variables: `ARD_BOT_LOGIN` (ard's App login, `<slug>[bot]`), `ARD_VERSIONING` (`on` for live), `RELEASE_APP_ID`, `RELEASE_BOT_NAME`, `RELEASE_BOT_EMAIL`.
- A `release` environment holding the secret `RELEASE_APP_PRIVATE_KEY`. The release App needs contents: write only, and must not be ard's App.

## Dry run to live

Every run is a dry run until `ARD_VERSIONING` is `on`. Check the plan in a dry run's step summary, then set the variable. `workflow_dispatch` is a dry run unless `dry_run` is false; `max_tags` (default 20) caps the tags one run creates, so raise it after reading the plan when a backlog is larger.

## Bumps and the module path

Label a PR `semver:minor` or `semver:major`; with neither it is a patch, and with both major wins. Go requires a `/vN` module path for N >= 2. A `semver:major` that would reach v2 stops the run (exit 2) until `go.mod` says `module .../v2`; relabel the PR or change `go.mod`, then rerun.

## Recovery

- A failed run stops at the first failure. Rerun it: the plan is rebuilt from the tags that exist, and a tag without a release gets a release-only backfill row.
- Never delete or move a tag. If a tag is wrong, fix forward with the next version.
- `ard version` prints the tag after `make build` on a tagged commit, and after `go install github.com/albertcbrown/ard/cmd/ard@vX.Y.Z`.
