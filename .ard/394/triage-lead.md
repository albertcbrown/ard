---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The request is a clear change: when a job slot is freed, the pool should signal the controller, and the controller should wake only the loops that have live demand, so the slot is refilled within seconds instead of sitting idle until the next poll. It touches the pool and controller packages and updates one paragraph in the proposal. The only triage planner configured is engineer, so engineer writes the plan and the engineer worker implements it.

## Routing
- triage:engineer: implementation steps for the pool signal, the demand-loop query, the controller's debounced per-loop wake, a check of when slots are released, and the fake-clock tests.
- worker engineer: code change in the pool and controller with unit tests, plus the proposal update.

## Open issues
- The change spans two packages and alters the controller's wake behaviour, which would normally go to an architect, but no architect role exists. The engineer's plan should spell out how the per-loop wake coalesces with scheduled ticks and SIGUSR1.
- The issue asks for a check that close-out writes its labels before the slot is released. If some path releases earlier, the plan needs the extra skip logic and a test for it.
