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
      The `instances` entry is described generically ("check the key name in that repository's docs"), not by name as the issue says.
      The worker could not verify the key name, so this is honest, but a maintainer should fill in the exact key once confirmed.
  - file: "docs/runbooks/onboard-repo.md"
    line: 116
    severity: info
    blocking: false
    message: |
      The runbook says ard has no token-cap setting. This is true today and the issue asks for token caps, so consider a follow-up issue for a real token cap.
---
The change is documentation only and matches plan.md and the issue: all eight runbook steps are present in order, the two-repo example sums correctly, and the proposal states the per-process limits and credentials rule. Approved.

## Checks
- Lint and coverage: skipped; docs-only change with no code lines.
- Verified by reading: `.github/workflows/bot-commits.yml` exists, the `max_pods`, `budget_usd_per_day` and `max_budget_usd` keys exist in the proposal's config block, `ARD_BOT_LOGIN` is used in the existing runbooks, and the dry-run state path `state-dryrun/<owner>/<repo>` matches `internal/state/dir.go`.
- Only the three docs files changed; nothing under `.github/` or `.ard/`.
