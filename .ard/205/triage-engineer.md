---
role: engineer
status: done
attempt: 1
---
Adds an optional `aid` executable to the reviewer role-directory contract. The reviewer loop runs it as its own read-only, credential-free, no-network executor job before the agent. Its validated output goes to the agent as an untrusted input, and the owner's blocking and failure rules are enforced in close-out. The plan has eleven steps, covering config discovery, the loop, close-out, the security-review starter aid and Dockerfile, and the proposal, with unit, fake-executor and Docker integration tests. Size L, risk high because it is security-sensitive and spans several packages.

## Open issues
- The worker has no network, so the pinned gosec, gitleaks and semgrep digests and the semgrep ruleset need a human to verify before merge.
- Host mode runs the aid without a sandbox, as decided; the plan documents that residual risk next to the existing host-mode warning.
- The step 4 check that the host executor does not pass the controller's environment (model credential, GitHub token) to the aid may need a small executor fix.
- Copying the starter aid into this repository's own reviewer config is a protected-path change that needs a human; it is not in the plan.
- The k8s role images stay with #245.
