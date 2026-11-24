---
role: lead
status: blocked
attempt: 1
---
The request is clear, but it cannot start yet. It needs the default ard-agent image from #55, which is still open in docs/TODO.md as parts #243, #244 and #245. The security-review starter's Dockerfile has to build on that image. Part A (#43, the security-review starter role) has already landed in internal/roles/reviewers/security-review.

## Routing
None until #55 lands. Then the plan should be: triage:engineer for implementation steps and tests, and worker engineer for the code change. No architect, product-manager, designer or documentation-planner triage role exists in this repository, so the engineer plan will also have to cover the cross-package design (config discovery, the reviewer loop, the executor and the role-directory contract).

## Open issues
- Waits for #55 (at least #243, the local ard-agent image, and #244, the default image under the local executor) so the aid Dockerfile has a base image to pin tools on.
- The issue lists five open questions a maintainer should settle before or during planning: whether a high-confidence aid hit blocks in code, whether a failed aid fails the job, whether host mode without a Dockerfile runs aid on the host or requires Docker, whether network access is allowed for vulnerability databases, and whether a gitleaks scan adds enough over the diff guard's built-in secret patterns.
- The starter files go under internal/roles/, which agents may change. Copying them into .ard/reviewers/security-review/ in this repository's own dogfooding config is a protected-path change that needs human approval.
