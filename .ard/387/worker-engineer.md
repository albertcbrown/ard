---
role: engineer
status: needs-human
attempt: 1
---
The default branch already contains this change, so there is nothing left to edit. The commit that references #387 added the end-to-end harness test and the proposal updates.

## Open issues
- `internal/servecmd/network_e2e_test.go` is already tracked and has the bounce-through-reviewers test and the three negative-path tests. `docs/proposal.md` already describes the bounce in the flowchart, label table, contract, gate, worker step 9, starter worker and configuration.
- A later job commit says it could not open the pull request. A maintainer should check whether #387 and #277 can be closed.
- The live `.ard/` prompt copies still need a human pull request, as the issue says.
