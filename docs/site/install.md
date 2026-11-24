# Install

ard runs on macOS and Linux. It needs:

- Go (to build), `make` and `git` 2.47.0 or newer. An older git fails some fetches.
- The `claude` CLI for the default agent.
- Docker, only for roles that ship a `Dockerfile` (the cve, docs and lint scanners).

Build from source:

```sh
git clone https://github.com/albertcbrown/ard
cd ard
make build
./bin/ard version
```

Put `bin/ard` on your `PATH`. A Homebrew cask is planned (#177); this page will point to it once it lands.
