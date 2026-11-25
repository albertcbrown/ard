---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a test-only fix for a flaky timing assertion in the GitHub transport's search rate-limit test. The change is confined to one test in internal/github. It changes no user-visible behaviour, public interface or stored data, so only the engineer needs to plan it.

## Routing
- triage:engineer: choose between asserting from the burst start and driving the limiter at fixed times (which may need a small test seam in the transport), and write a test plan that covers the stress run and the limiter-mutation check.
- worker engineer: rewrite the test so it does not depend on the wall clock, and keep all three checks.

## Open issues
- If the planner picks the fixed-time approach, the transport may need a small injectable clock or reservation hook. That is a minor production-code change inside internal/github.
