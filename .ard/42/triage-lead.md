---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds the rules that pick which reviewers run on a worker pull request: by default every reviewer runs, a reviewer's when: rule in the configuration can narrow that, a worker can override the set, and the lead can ask for extra reviewers. The reviewer loop it depends on (#35) has already landed, and the configuration loader already parses a when: rule, so the work belongs in the reviewer selection logic and its tests. The engineer is the only triage planner in this repository.

## Routing
- triage:engineer: plan the selection logic across the config and reviewer packages, and write one test for each rule.
- worker engineer: code change with unit tests.

## Open issues
- The repository has no architect or documentation-planner role. The change touches documented configuration behaviour (when: rules and the worker reviewers: override), so a maintainer may want to check that the proposal doc and the configuration reference stay in step.
