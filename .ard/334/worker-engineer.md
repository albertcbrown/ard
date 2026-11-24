---
role: engineer
status: done
attempt: 1
---
Bumps the docs-site toolchain so pymdown-extensions resolves to a release that fixes the ReDoS advisory. mkdocs-material 9.5.50 capped it below 11, so material is bumped too.

## Changes
- docs/site/requirements.txt: mkdocs-material 9.5.50 to 9.8.2, and a new explicit floor `pymdown-extensions>=11.0.1` so the fix is visible to the scanner.

## Testing
- pip dry run confirmed 9.5.50 conflicts with pymdown-extensions>=11.0.1, and 9.8.2 resolves.
- In a scratch venv outside the repo: `pip install -r docs/site/requirements.txt` installed pymdown-extensions 13.1.2.
- `make docs-check` passes: strict mkdocs build exits 0 and `go test ./internal/docscheck/...` is ok.

## Open issues
- The strict build prints a notice that MkDocs 2 is coming. It is not an error and is unrelated to this change.
