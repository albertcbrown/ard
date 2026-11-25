---
role: engineer
status: done
attempt: 1
---
Adds a version floor for pymdown-extensions (10.16.1 or later) to the docs site requirements, so the docs toolchain no longer installs a version with the Figure Capture ReDoS advisory.

## Changes
- docs/site/requirements.txt: comment line and `pymdown-extensions>=10.16.1`, in the same style as the certifi pin.

## Testing
Installed the requirements into a fresh virtualenv outside the repo: resolved without conflicts, pymdown-extensions 10.21.3 (below 11). Ran `mkdocs build --strict` with the repo's mkdocs.yml, output written outside the repo. No code tests apply.

## Open issues
None.
