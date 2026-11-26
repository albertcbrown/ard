# Install

ard runs on macOS and Linux. It needs:

- Go (to build), `make` and `git` 2.47.0 or newer. An older git fails some fetches.
- The `claude` CLI for the default agent.
- Docker, only for roles that ship a `Dockerfile` (the cve, docs and lint scanners).

## Homebrew

```sh
brew install --cask albertcbrown/tap/ard
ard version
```

Upgrade with `brew upgrade --cask albertcbrown/tap/ard`. `brew uninstall --cask ard` removes the binary and leaves `~/.ard/` alone. The binary is not yet signed or notarized, so the cask clears the macOS quarantine flag after install (`hooks.post.install`); see the Guardrails section of the proposal.

## Build from source

```sh
git clone https://github.com/albertcbrown/ard
cd ard
make build
./bin/ard version
```

Put `bin/ard` on your `PATH`.