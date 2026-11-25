# Security model

Agents read text that strangers can write, so the controls are in code and permissions, not in prompts. The full reasoning is in the [proposal](https://github.com/albertcbrown/ard/blob/main/docs/proposal.md).

- **Agents hold no GitHub token.** Only the controller talks to GitHub and runs git side effects. An agent reads and writes files in a worktree, and the controller decides what to commit.
- **Untrusted input is passed as files.** Issue text and comments arrive as `.ard-input/<issue#>/` files that are never committed, not inside the prompt. Role prompts tell agents to treat them as data.
- **The GitHub App is narrow.** It lacks the `workflows` permission, so GitHub rejects any push that edits `.github/workflows/`. A guard also refuses all of `.github/`.
- **Protected paths.** A pull request touching `merge.protected_paths` needs a human approval.
- **Write allowlists.** A role's output is committed only if every path is allowed (see [Roles](roles/index.md)).
- **Log redaction.** Before a log is committed, close-out removes known secret values and replaces the host's own paths (the worktree, the home directory, the user name) with fixed placeholders, so logs do not reveal the machine (#156). A log that still fails the secret scan is held back. Logs are capped at 1 MiB.
- **Host mode and Docker.** A role without a `Dockerfile` runs as a host subprocess with your user's access, and ard logs `executor.host_untrusted`. That is acceptable on a machine you control with repositories you trust. A role with a `Dockerfile` runs in a container (#84). Use Docker for anything that reads untrusted code.
- **The bot-commit audit.** The bot bypasses branch protection on `main`, so a workflow audits main's first-parent history and fails if a bot commit changes anything but regular files under one `.ard/<issue#>/` directory.
- **The GitHub client does not follow redirects**, so a renamed or transferred repository is an error rather than a silent write elsewhere.
