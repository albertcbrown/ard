---
role: engineer
status: done
attempt: 1
---
Adds a new controller loop that sends a pull request with a changes verdict back to the worker, counts each round in the existing local event log (no migration), reruns only the reviewers that asked for changes, and hands the issue to a human at the round cap. Worker and reviewer loops get small input additions so they see the findings. Size L, risk medium.

## Open issues
- The reviewer prompt under .ard/reviewers/ must be edited by a maintainer so reviewers report which earlier findings are resolved. The controller will supply the earlier findings as an input file, but agents may not change that prompt. The worker prompt may need a short line on the new review feedback input, which is also a maintainer edit.
- The plan relaxes the mark-ready head check for approving reviewers that were not asked to rerun, because without that a PR could never become ready after a fix round. A maintainer should confirm this is acceptable.
- CLAUDE.md is a protected path; its layout list should gain the new package, so a maintainer may need to make that line edit.
- The plan waits for any running reviewer on the PR before starting a round, so all findings arrive together. This is a design choice the proposal does not state.
