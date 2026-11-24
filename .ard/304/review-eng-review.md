---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "docs/runbooks/onboard-repo.md"
    line: 131
    severity: low
    blocking: false
    message: |
      The `instances` entry is described generically ("check the key name in that repository's docs") instead of by name.
      A maintainer should fill in the exact key once it is confirmed.
  - file: "docs/runbooks/onboard-repo.md"
    line: 116
    severity: info
    blocking: false
    message: |
      The runbook says ard has no token-cap setting. This is true today; consider a follow-up issue for a real token cap.
---
The change is documentation only and matches the issue: the eight runbook steps are in order, the two-repo example sums correctly, and the proposal states the per-process limits and credentials rule. Approved; the two earlier non-blocking notes remain.

## Checks
- Lint and coverage: skipped; docs-only change with no code lines.
- Read the three changed docs files; nothing under `.github/` or `.ard/` changed.

## Previous findings
- docs/runbooks/onboard-repo.md (instances key named generically): still present, non-blocking.
- docs/runbooks/onboard-repo.md (no token-cap setting): still present, info only.
