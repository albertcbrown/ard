# Getting started

This takes a repository from nothing to a first triaged issue. Run the `ard` commands from a clone of the target repository, or pass `--repo owner/name`.

## 1. Create and install the GitHub App

ard acts as a GitHub App, never as a personal token. The quickest way to create one with the right permissions is the manifest flow:

```sh
ard setup --create-gh-app --dry-run   # preview the manifest and URL; creates nothing
ard setup --create-gh-app
```

ard opens a page on a loopback-only local server, you confirm the App on GitHub once, and ard stores the private key in `~/.ard/keys/<slug>.pem` (mode 0600; change it with `--key-file`, overwrite with `--force`). It then prints the install link, waits for you to install the App on the repository, prints the `ARD_GITHUB_*` lines to add to ard's environment, and runs the normal setup checks. The App name defaults to `ard-<owner>-<repo>`; override it with `--app-name`.

### Fallback: create the App by hand

Create an App you own and give it exactly these repository permissions:

| Permission | Access |
| --- | --- |
| Contents | Read and write |
| Issues | Read and write |
| Pull requests | Read and write |
| Checks | Read and write |
| Commit statuses | Read |
| Metadata | Read |

Do **not** grant `workflows`. Without it GitHub itself rejects any push that edits `.github/workflows/`. Install the App on the target repository, then note the App ID and installation ID and download the private key (a `.pem` file; keep it mode 0600).

Give `ard serve` the App through its environment: `ARD_GITHUB_APP_ID`, `ARD_GITHUB_INSTALLATION_ID` and the key as `ARD_GITHUB_PRIVATE_KEY_FILE` (a path) or `ARD_GITHUB_PRIVATE_KEY`.

## 2. Let the App push to main

ard commits its own files under `.ard/<issue#>/` straight to `main`. If `main` has a ruleset, add the App to the ruleset's bypass list. Without that, pushes fail with a `GH013` ruleset rejection.

## 3. Audit the bot's commits

Set the repository variable `ARD_BOT_LOGIN` to the App's `<slug>[bot]` login, and add a workflow that fails when a bot commit changes anything outside a single `.ard/<issue#>/` directory. The example is [.github/workflows/bot-commits.yml](https://github.com/albertcbrown/ard/blob/main/.github/workflows/bot-commits.yml). The App cannot write workflows, so you add this one yourself.

## 4. Initialise the repository

```sh
ard init
```

`ard init` opens a pull request with the starter `.ard/` tree: `config.yaml` and the starter role directories. It also writes `.ard/ard-init.lock`, the hash of each starter file it wrote. Review and merge it. Rerunning `ard init` after an upgrade updates only files you have not edited, never touches an edited file (it prints a diff against the starter instead) and never deletes. Then:

```sh
ard labels sync
ard config check
```

`ard labels sync` creates the `ard:` labels and never deletes one. `ard config check` prints the resolved configuration and reports every problem with its file and key.

## 5. Store the model credential

Set `CLAUDE_CODE_OAUTH_TOKEN` (a subscription token from `claude setup-token`) or `ANTHROPIC_API_KEY` in the environment of `ard serve`. By default the token wins when both are set; `ARD_AGENT_AUTH` set to `oauth` or `apikey` forces one. The value is passed to the agent job and is never logged. If you use a subscription, turn off extra usage in the account so a usage limit is not billed as overage.

## 6. Start serving

Try a dry run first. It logs every side effect without performing it:

```sh
ard serve --dry-run
```

Then start it for real:

```sh
ard serve
```

## 7. File and label a first issue

File an issue describing a small change. With the default `intake.mode: label`, add the `ard:pending` label to admit it. Within one triage interval (15 minutes by default) the lead writes `.ard/<issue#>/plan.md` and the issue gains a triage-done label. If `require_approval` is on, add `ard:approved` to let a worker start.

If nothing happens, see [Troubleshooting](troubleshooting.md).
