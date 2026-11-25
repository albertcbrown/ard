---
role: engineer
status: needs-human
attempt: 1
---
The request spans role-file parsing, close-out outcomes and label phases, the triage input file, lead re-arm with persisted round state, a gate inputs check with approval changes, a loop guard, starter prompts, an end-to-end harness test and proposal updates. Counting tests and docs, that exceeds one reviewable worker change (XL), so no plan was written and a split is proposed.

## Open issues
- Proposed split, in order:
  1. Worker signal: add the `needs-network` status and `network:` list to `internal/closeout/rolefile.go`, parse into `Plan.Network` after redaction in `prepare.go`, add `OutcomeNeedsNetwork` in `plan.go` (`Build`, including the Reaper case) and render the comment. In this part the outcome ends `needs-human` with the list in the comment, and nothing is pushed.
  2. Bounce: the `closeout.NetworkRound` helper, the label request and remove phases in `closeout.go` (modelled on `LeadWorker`), the `network-request.md` triage input, the one-round loop guard, the branch-exists and already-pushed checks, and the starter engineer and worker prompt copies in `internal/roles/`.
  3. Re-run and gate: lead re-arm and persisted round state in the record, the re-run list for other triage roles, the `inputs:` frontmatter check and *waiting for inputs* status in `internal/triage/fanin.go`, and the per-round approval marker with forced approval.
  4. The end-to-end harness test and proposal updates.
- The lead also noted that no architect role is available. A maintainer may want to confirm the persisted round-state design before part 3.
- Live prompts under `.ard/workers/` and `.ard/triage/` are protected and need a human PR.
