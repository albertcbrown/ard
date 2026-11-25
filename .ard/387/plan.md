# Plan for #387

## Implementation

### Steps
1. New `internal/servecmd/network_e2e_test.go` (new, package `servecmd`): a combined harness `newNetworkE2E(t, scripts)` modelled on `newWorkerE2E` (`worker_e2e_test.go:66`), `newE2E` (`triage_e2e_test.go:73`) and `newReviewE2E` (`reviewer_e2e_test.go:76`). It shares one `wiring`, one fake GitHub (`fake.New`), one bare remote from `gittest.NewRemote`, one store and one pool, with `closeout.Deps.Network` on as `servecmd` sets it (`reaping.go:64`). Roles: `lead`, `engineer` (triage), `engineer` (worker, `network: false`) and `eng-review`. Agents are fake `claude` shell scripts selected by role and a per-test counter file; no network is used. Reuse existing helpers (`itoa`, `has`, `e2eToken`); do not edit the existing test files, add small helpers in the new file.
2. Scripted main path in `TestNetworkBounceEndToEnd`, one tick-driven phase at a time with assertions after each:
   - worker answers `status: needs-network` with a `network:` entry; expect `ard:triage:engineer` requested, and `ard:worker:engineer`, `:running` and `ard:triage:engineer:done` removed, with no branch and no PR;
   - the engineer job sees `network-request.md`, writes `### Network inputs` into `plan.md` and `inputs:` (human provider, path under `testdata/`), status `done`; expect `ard:triage:lead:done` cleared and `ard:pending` set;
   - the lead re-runs, re-requests the other triage roles that had finished, and they re-run (add a second fake triage role to cover the `Rerun` list);
   - the gate holds with a "waiting for inputs" comment while the file is missing from main, and there is a `wait` gate event;
   - commit the file to main (`gittest.Run ... push origin HEAD:main`); expect the `inputs-ready` event and a fresh "ready for approval" comment with `round=1`, even though `RequireApproval` is false in a variant;
   - an earlier approval is stale; add `ard:approved` after the new marker; expect `ard:worker:engineer`;
   - worker runs, pushes the branch, opens a draft PR; reviewer runs; expect review labels on the PR and the review posted.
3. Negative tests in the same file:
   - `TestNetworkSecondRoundNeedsHuman`: a worker answering `needs-network` again after a round ends `ard:needs-human`, the comment lists the entries, and the engineer is not requested.
   - `TestNetworkBranchExistsNeedsHuman`: seed `worker/engineer-<n>-<slug>` on the remote first, so the bounce is refused and the outcome is `needs-human` with the list.
   - `TestNetworkWorkerInputWithNetworkFalse`: the engineer writes an input with `provider: worker` and the worker role has `network: false`; expect `needs-human` at the gate, with no `ard:worker:engineer` added.
4. `docs/proposal.md`: bring each section parts 1 to 3 left out up to date, written as shipped behaviour (no "for now" or "part N adds"):
   - Architecture flowchart (line 25): add an edge `W -->|"needs-network: one round"| T`; adjust the paragraph under it.
   - Label state machine: add a short paragraph after the table on the bounce (worker removes, engineer requested, lead `:done` cleared, `ard:pending` re-added) and mention it in the `ard:pending` and `ard:triage:<role>` rows.
   - `.ard/` contract: fold the line 134 text ("For now the outcome ends like needs-human" and "Part 2 adds ... part 3 switches it on") into the shipped description; document the `### Network inputs` plan section and `inputs:` key next to the role-file schema (lines 149 to 160).
   - Triage loop and gate (around 263 to 303): the starter lead and engineer paragraphs (lines 279 to 281) and the gate paragraph need the re-run, the inputs check, "waiting for inputs" and the per-round approval.
   - Worker loop: step 9 (line 364) gets the `needs-network` outcome, and the starter worker paragraph (line 366) gets the bounce, the one-round limit and the `network: false` case.
   - Configuration: the `network` paragraph (line 510) gets how `network: false` interacts with the bounce and worker-provided inputs.
   Keep the text accurate by reading `internal/closeout/network.go`, `internal/triage/fanin.go` and `internal/servecmd/reaping.go` first; do not describe anything the code does not do. Also run `go test ./internal/docscheck/...` for the docs check.

### Test plan
- Command: `go test ./internal/servecmd/ -run Network` then `make test` (that is `go test ./...`); also `make lint` if present, since the starter worker runs `make lint test`.
- Cases: the four tests above (`TestNetworkBounceEndToEnd`, `TestNetworkSecondRoundNeedsHuman`, `TestNetworkBranchExistsNeedsHuman`, `TestNetworkWorkerInputWithNetworkFalse`), all in `internal/servecmd/network_e2e_test.go`.
- Edge cases: missing input blocks the gate until it lands on main; an approval given before the input lands still counts once it does; a stale approval before the engineer's new `:done` does not.
- Failure cases: no real network access (fake agents and `file://` remote only); assert no test touches the host's network.

### Risk
Low. The change is a test file and docs only. No protected path is touched (`.ard/triage/`, `.ard/workers/`, `.ard/reviewers/`, `.ard/config.yaml` and `.github/` stay unchanged; the live prompt copies need a human PR, as the issue says). Main risk is test flakiness from the multi-loop tick ordering; drive each loop by explicit ticks, not timers.

### Size
L, roughly 500 to 900 changed lines. Files: `internal/servecmd/network_e2e_test.go` (new) and `docs/proposal.md`.
