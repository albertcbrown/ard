---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The blocker is gone: the local ard-agent image (#243) and the default image under the local executor (#244) are both on main, and the owner has answered all five open questions. This issue can move to planning. It adds an optional `aid` executable to the reviewer role-directory contract, runs it as its own executor job before the reviewer agent, and ships a pinned security-review aid, so the engineer plan has to cover config discovery, the reviewer loop, the executor, the starter role and the proposal.

## Routing
- triage:engineer: implementation steps and tests across config discovery (`Role.AidPath`, `ard config check`, `roles.ExecutableNames`), the reviewer loop and executor job (read-only worktree, empty credential environment, no network under Docker, its own timeout), JSONL validation and redaction, the owner's rules for blocking and failed aids, the security-review starter aid and Dockerfile, and the proposal update.
- worker engineer: Go code, fake-executor and Docker integration tests, the starter role files and docs.

## Open issues
- This repository has no architect, product-manager or documentation-planner triage role. The engineer plan must therefore also settle the cross-package design and the proposal changes: the role-directory contract, Reviewer loop step 3, the reviewer table, and the host-mode residual-risk note next to #84's.
- The owner's decisions in the comments change the scope from the issue body. A gitleaks hit on an added line becomes a blocking high finding that ard injects in code. A security review whose aid failed can only return changes. Host mode without a Dockerfile runs aid on the host. The aid gets no network access. gitleaks is included. The plan should follow these decisions.
- #245 (role image builds for k8s) is still open. This change should target the local executor and leave k8s role images to #245.
- The starter files belong under internal/roles/. Copying the aid into this repository's own .ard/reviewers/ config is a protected-path change and needs a human. The dogfooding config has only eng-review today.
- The pinned tool versions and the vendored semgrep ruleset need checksums or digests. A worker without network access may not be able to fetch them, so the plan should say how to get them.
