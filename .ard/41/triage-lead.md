---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds the review feedback loop: a reviewer's changes verdict sends the pull request back to the worker, round after round, until the configured cap hands the issue to a human. The reviewer loop it depends on has already landed and the cap setting is already in the configuration, so the work can start now. The engineer is the only triage role and the only worker in this repository.

## Routing
- triage:engineer: implementation steps across the worker loop, reviewer loop and local state (round counter, feeding reviewer logs and PR comments to the worker, rerunning only the reviewers that asked for changes, stopping at the cap), plus the end-to-end test.
- worker engineer: controller code change with tests.

## Open issues
- The change touches several controller packages and local state, but no architect role exists here, so the engineer's plan should settle where the round count is stored.
- Reviewers need to report which earlier findings are resolved, and workers need to use review feedback. If either prompt under .ard/reviewers/ or .ard/workers/ has to change, a maintainer must make that edit, because agents may not change those paths. The worker can only add the inputs the controller provides.
