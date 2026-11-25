---
role: engineer
status: done
attempt: 1
---
Makes the flaky test cancel its context from inside the `flaky` test double on the first transient push failure, rather than on a wall-clock timer, so cancellation always lands in the backoff wait. The plan has three steps, all in one test file. Size S, risk low.

## Open issues
None.
