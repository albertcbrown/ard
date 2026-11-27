---
role: engineer
status: done
attempt: 1
---
Plans a controller-side path for a conflict round whose trial merge is clean: the worker pushes the merge of main through the existing guard and code lane with no agent job, then closes the round. Size M, risk medium because it changes the push and close-out path.

## Open issues
- The worker should check that close-out accepts a job with no agent step and no role file; step 3 of the plan says what to do if it does not.
