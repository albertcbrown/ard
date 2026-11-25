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
- A tag ruleset on `v*` that only the release App may bypass.
- For Homebrew: the secret `TAP_GITHUB_TOKEN` in the same `release` environment, and the `albertcbrown/homebrew-tap` repository (see Homebrew release).

The steps below set these up once. They use `ard-release` as the App's name; use any name that is free, and substitute it throughout.

## Create the release App

`version.yml`'s `tag` job mints a short-lived token from this App (`actions/create-github-app-token`) to push tags and create releases. It is a separate App from ard's so that ard's own token can never create a tag; the job refuses to run if the two are the same (it compares the App's slug with `ARD_BOT_LOGIN`).

1. Open <https://github.com/settings/apps/new> (for an organization: the organization's Settings → Developer settings → GitHub Apps → New GitHub App).
   - **GitHub App name:** `ard-release`. **Homepage URL:** the repository URL.
   - **Webhook:** clear **Active**. The App receives no events.
   - **Repository permissions:** **Contents: Read and write**. Leave every other permission at **No access** (Metadata stays read-only, as GitHub requires).
   - **Where can this GitHub App be installed?** **Only on this account**.
   - Select **Create GitHub App**.
2. On the App's page, note the **App ID**. Under **Private keys**, select **Generate a private key**; the browser downloads a `.pem` file.
3. Select **Install App**, choose the account, then **Only select repositories** → `albertcbrown/ard`. Install it on nothing else.
4. Set the repository variables. The tagger email is the bot's noreply address, which needs the bot account's numeric user ID:

   ```
   gh api '/users/ard-release[bot]' -q .id
   gh variable set RELEASE_APP_ID    -R albertcbrown/ard -b '<app-id>'
   gh variable set RELEASE_BOT_NAME  -R albertcbrown/ard -b 'ard-release[bot]'
   gh variable set RELEASE_BOT_EMAIL -R albertcbrown/ard -b '<bot-user-id>+ard-release[bot]@users.noreply.github.com'
   ```

   Check that `ARD_BOT_LOGIN` is also set to ard's own App login (`gh variable list -R albertcbrown/ard`).
5. Create the `release` environment and store the key only there, then delete the downloaded file:

   ```
   gh api -X PUT repos/albertcbrown/ard/environments/release
   gh secret set RELEASE_APP_PRIVATE_KEY -R albertcbrown/ard --env release < ~/Downloads/ard-release.<date>.private-key.pem
   rm ~/Downloads/ard-release.<date>.private-key.pem
   ```

   Optionally restrict the environment's deployment branches and tags to `main` and `v*` (Settings → Environments → release), so only runs from those refs can read its secrets.

To rotate the key, generate a new one on the App page, run the `gh secret set` line again with it, then delete the old key on the App page.

## Protect the tags

A tag ruleset keeps tags immutable and lets only the release App create them. `ARD_BOT_LOGIN`'s App, workflows using `GITHUB_TOKEN` and people without bypass cannot create, move or delete a `v*` tag.

```
gh api -X POST repos/albertcbrown/ard/rulesets --input - <<'EOF'
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "rules": [ { "type": "creation" }, { "type": "update" }, { "type": "deletion" } ],
  "bypass_actors": [ { "actor_id": <app-id>, "actor_type": "Integration", "bypass_mode": "always" } ]
}
EOF
```

`actor_id` is the release App's **App ID** (the same number as `RELEASE_APP_ID`). The same ruleset can be made in Settings → Rules → Rulesets → New tag ruleset: target `v*`, enable **Restrict creations**, **Restrict updates** and **Restrict deletions**, and add the App under **Bypass list**. Check it with `gh api repos/albertcbrown/ard/rulesets`.

The seed tag (above) must exist before the ruleset, or an admin must be added to the bypass list while creating it and removed afterwards.

## Turn it on

1. Run a dry run and read the plan in the run's step summary:

   ```
   gh workflow run version.yml -R albertcbrown/ard
   gh run watch -R albertcbrown/ard "$(gh run list -R albertcbrown/ard -w version.yml -L 1 --json databaseId -q '.[0].databaseId')"
   ```

2. Set `ARD_VERSIONING` to `on` (`gh variable set ARD_VERSIONING -R albertcbrown/ard -b on`).
3. Run live, raising `max_tags` when the dry run planned more than 20 tags:

   ```
   gh workflow run version.yml -R albertcbrown/ard -f dry_run=false -f max_tags=60
   ```

   A failure that names a variable or the App points at the step above that sets it; fix it and rerun (see Recovery).

## Homebrew release (release.yml)

`.github/workflows/release.yml` runs on each `v*` tag `version.yml` pushes (and on `workflow_dispatch` with a `tag`). It builds the four archives with GoReleaser (`.goreleaser.yaml`), uploads them and `checksums.txt` to the tag's existing release, and updates `Casks/ard.rb` in `albertcbrown/homebrew-tap`.

- It does nothing until `ARD_VERSIONING` is `on` **and** the `release` environment holds the secret `TAP_GITHUB_TOKEN`. Without the token it logs a notice and stops.
- `TAP_GITHUB_TOKEN`: a fine-grained token (or a second App's token) with **contents: read and write on `albertcbrown/homebrew-tap` only**. Store it only in the `release` environment.
- Create `albertcbrown/homebrew-tap` (public, with a `Casks/` directory) and protect its `main`.

To set up the tap and its token:

1. Create the tap repository with a `Casks/` directory:

   ```
   gh repo create albertcbrown/homebrew-tap --public --description "Homebrew tap for ard"
   gh repo clone albertcbrown/homebrew-tap && cd homebrew-tap
   mkdir Casks && touch Casks/.gitkeep && git add Casks && git commit -m "Add Casks directory" && git push origin HEAD:main
   ```

2. Create the token at <https://github.com/settings/personal-access-tokens/new>: **Resource owner** `albertcbrown`, **Repository access** → **Only select repositories** → `albertcbrown/homebrew-tap`, **Repository permissions** → **Contents: Read and write**, nothing else. Pick an expiry and note it; the release stops with a push error when it lapses.
3. Store it in the `release` environment only. `gh` prompts for the value, so it stays out of shell history:

   ```
   gh secret set TAP_GITHUB_TOKEN -R albertcbrown/ard --env release
   ```

4. Protect the tap's `main` (Settings → Rules → New branch ruleset: block force pushes and deletions). GoReleaser commits `Casks/ard.rb` directly, so do not require pull requests there.
5. Rotate the token before it expires: create a new one and run step 3 again, then revoke the old one.
- On a tag push only the newest `v*` tag is released, so a backfill of many tags publishes once. To build an older tag: `gh workflow run release.yml -R albertcbrown/ard -f tag=vX.Y.Z`.
- One release runs at a time (`concurrency: release`, never cancelled).
- Check locally first: `make release-check` (snapshot, publishes nothing).

## Dry run to live

Every run is a dry run until `ARD_VERSIONING` is `on`. Check the plan in a dry run's step summary, then set the variable. `workflow_dispatch` is a dry run unless `dry_run` is false; `max_tags` (default 20) caps the tags one run creates, so raise it after reading the plan when a backlog is larger.

## Bumps and the module path

Label a PR `semver:minor` or `semver:major`; with neither it is a patch, and with both major wins. Go requires a `/vN` module path for N >= 2. A `semver:major` that would reach v2 stops the run (exit 2) until `go.mod` says `module .../v2`; relabel the PR or change `go.mod`, then rerun.

## Recovery

- A failed run stops at the first failure. Rerun it: the plan is rebuilt from the tags that exist, and a tag without a release gets a release-only backfill row.
- Never delete or move a tag. If a tag is wrong, fix forward with the next version.
- `ard version` prints the tag after `make build` on a tagged commit, and after `go install github.com/albertcbrown/ard/cmd/ard@vX.Y.Z`.
