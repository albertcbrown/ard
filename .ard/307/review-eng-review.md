---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "docs/site/install.md"
    line: 27
    severity: low
    blocking: false
    message: |
      The file now ends without a trailing newline (the patch shows "No newline at end of file").
      Add a final newline.
---
The docs-only change covers the README Install section, the release runbook and the Gatekeeper guardrail. They match `.goreleaser.yaml` and `release.yml`: keep-existing mode, the post-install quarantine hook, the git dependency, the `release` concurrency group, the newest-tag skip, and the `workflow_dispatch` `tag` input. Approved.

## Checks
- Lint: skipped; the change touches only Markdown and I found no Markdown lint entry point to run.
- Diff coverage: skipped; the change adds no code.
- Facts cross-checked by reading `.goreleaser.yaml` and `.github/workflows/release.yml`. Nothing under `.ard/*.md` is changed.
