---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "deploy/test-release.sh"
    line: 53
    severity: low
    blocking: false
    message: |
      The check compares `ard version` with the tag from dist/metadata.json, while the issue says the snapshot version. With the config's `-X main.version={{.Tag}}` these agree, so the check is sound. If the ldflags ever change to `.Version`, the comparison will need updating.
---
The change matches the issue: the GoReleaser v2 config has four targets, a keep-existing release and a cask with a quarantine postflight, and no secret is in the file. The release-check script covers the required assertions and the docs are updated. Approved.

## Checks
- Lint: the Makefile's lint target was not run; the diff holds no Go code, only YAML, shell and docs.
- Diff coverage: skipped, since no Go lines were added.
- `make release-check` was not run because goreleaser is not installed here, and I did not run a command the change added. I read the script by hand: its logic, the `ard %s` version format in internal/cli/cmd_version.go and the `main.version` variable all line up.
