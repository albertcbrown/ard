---
role: engineer
status: done
attempt: 1
---
Adds an offline end-to-end harness test for the network inputs back-route and brings docs/proposal.md up to date so it describes the finished feature. The code is already on this issue's worker branch from the first job, which failed only when opening the pull request; no further edits were made.

## Changes
- internal/servecmd/network_e2e_test.go (new): a combined harness with fake agents. TestNetworkBounceEndToEnd covers needs-network, the bounce to the triage engineer, the lead and other triage roles re-running, the gate holding for a missing human input, the input landing on main, a fresh "ready for approval", approval, worker, PR and reviewers. Three negative tests cover a second round, an existing branch, and a worker-provided input with a `network: false` worker.
- docs/proposal.md: flowchart worker-to-triage edge, label state machine, `.ard/` contract, triage loop and gate, worker step 9 and the starter worker, and configuration now describe the shipped behaviour.

## Testing
Ran `go test ./internal/servecmd/ -run Network`; it passes. The first job also ran the docs check and the full test suite. No network is used by the tests.

## Open issues
- The live `.ard/` prompt copies still need a human pull request, as the issue says.
