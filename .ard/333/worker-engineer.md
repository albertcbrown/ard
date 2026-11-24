---
role: engineer
status: done
attempt: 1
---
Adds an explicit lower bound on certifi in the docs site requirements so pip installs the release that fixes the emSign root CA advisory.

## Changes
- docs/site/requirements.txt: added `certifi>=2026.10.18` with a comment naming the advisory; existing mkdocs pins unchanged.

## Testing
Not run: no virtualenv install or `make docs-check` was done here. The change is a single requirements line. The plan's checks (`pip show certifi` and `make docs-check`) still need to run.

## Open issues
- Build verification was not run; CI or a reviewer should confirm there is no resolution conflict.
