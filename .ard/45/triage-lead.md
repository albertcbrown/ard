---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This issue adds a starter product-manager-review reviewer prompt, its starter config override and tests under internal/roles/, plus edits to the docs. Both dependencies have now landed: the product-manager triage role is in internal/roles/triage/, and config.When.Matches evaluates when rules with OR semantics. The work goes to the engineer for planning and implementation.

## Routing
- triage:engineer: check the existing plan against current main. The shared reviewer test helpers may need to account for the license-review and security-review starters that landed since the plan was written. Then confirm the steps and the test run.
- worker engineer: new embedded prompt, starter override, Go tests and doc edits. None of these files are in the protected role directories under .ard/.

## Open issues
- The plan edits CLAUDE.md, which is a protected merge path, so the PR will wait for a human approval. A human should also review the prompt text itself.
- internal/roles/reviewers/ now also holds license-review and security-review. The test that lists the reviewer roles and the shared helpers must include them, not only eng-review.
- Turning the role on in this repository's own .ard/ is a separate human follow-up PR. It needs a product-manager triage role under .ard/triage/, which this repository does not have yet.
- The Done-when check, an end-to-end run on a scratch repo, is manual.
