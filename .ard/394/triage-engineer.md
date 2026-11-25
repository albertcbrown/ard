---
role: engineer
status: done
attempt: 1
---
Adds a freed-slot signal and a demand-loop query to the pool, and a debounced per-loop wake in the controller, wired up in the serve command, plus a proposal paragraph update. The plan has seven steps and fake-clock tests for the pool and controller. Size M, risk medium because it touches the controller's goroutines and the pool's release path.

## Open issues
- The pool is built in a startup hook after the controller is created, so the plan passes the pool through lazy accessor functions in the controller's dependencies instead of a channel value.
- Every release path was read: the three job runners release by `defer` after close-out, so no earlier release was found. A guard test is still in the plan.
