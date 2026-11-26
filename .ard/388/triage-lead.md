---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This narrows a false positive in the built-in secret scanner. The generic-assignment rule will skip values shaped like upper-case snake-case environment variable names. The change stays inside internal/redact (rules.go plus its table tests), with a one-line note in the proposal's secret-scan text, so only the engineer planner is needed.

## Routing
- triage:engineer: plan the narrow skip in the generic-assignment placeholder/code-reference checks, check the other rules' formats and the test corpus for secrets in the same shape, and set out the table tests (new negatives, existing positives, mixed- and lower-case values still reported).
- worker engineer: a change to one package, with unit tests and a small doc note.

## Open issues
- This loosens a security control that the log scan, the committer scan and the diff guard all share. The reviewer should confirm the pattern stays narrow (it needs an underscore, allows no lower case, has a length cap) and that no existing true-positive test changes.
- Credential-shaped test fixtures must be built at run time, or the diff guard will hold the worker's own change.
