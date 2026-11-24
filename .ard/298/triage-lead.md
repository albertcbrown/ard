---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Makes the Docker executor run a worker's test step with networking disabled when the role sets network to false, while agent steps stay advisory. The prerequisite part appears to have landed already: the executor's Network type, including the NetworkNone value, and the advisory wiring in the agent job builder are in the tree. That leaves a narrow change in two files plus tests, so the engineer can plan it alone.

## Routing
- triage:engineer: steps for the Docker run arguments and the worker test-step job, plus the table test and the worker test with the executor fake.
- worker engineer: Go code change with unit tests.

## Open issues
- No product-manager, architect or documentation-planner role exists in this repository. Turning network false into a real restriction for the test step changes documented behaviour: the configuration rendering and the comments in internal/config/types.go still call the setting advisory. The engineer's plan should cover updating that wording.
- A previous triage run for this issue failed when the controller pushed its commit, not because of the routing.
