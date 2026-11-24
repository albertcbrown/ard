---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Part 4 of the Kubernetes executor adds two things. One is scoped read-only installation token minting and revocation in the GitHub package. The other is a new controller-to-pod contract package covering paths, Secret items, the input manifest and the result route. The work is clear and the tarball part it builds on is merged, so it goes to the engineer for implementation steps and then to the engineer worker.

## Routing
- triage:engineer: turn the latest plan (design revision 3) into concrete steps and tests. That plan covers the token mint and revoke calls, the PAT refusal, the contract constants, the manifest size cap and key matching.
- worker engineer: code change in the GitHub package and a new package, with httptest-based unit tests and a docs/layout.md entry.

## Open issues
- The issue body still describes the superseded design: an HTTP job API on the controller, a key table and TLS. The newest comment replaces it with design revision 3. The plan should follow the newest comment, and a maintainer may want to update the body to match.
- This change mints GitHub credentials, so the scope of the request body (contents read only, repositories limited to the installation's owner) needs a careful review on the PR.
- The issue depends on two earlier parts. The tarball part (part 3) is merged. The part-1 decision issue is referenced only for owner decisions that the comments say are already made.
- Only the engineer triage role exists, so no product, architecture or documentation planning role was requested, even though this adds a new package.
