---
role: engineer
status: done
attempt: 1
---
Adds a transient error class to gitx, a separate bounded backoff retry in the committer's main, code and init lanes that checks whether the push already landed, and a new retryable close-out cause. Size M to L, risk medium because the shared retry loop and the close-out mapping change.

## Open issues
- No existing close-out cause fits, so the plan adds a new one, `push_transient`. A maintainer may prefer another name.
- The protected-branch rejection case from the comment is left out of this change. Its retry count and needs-human wording are a policy decision, so it should be a separate issue.
- A job that is already a retry gets no further automatic retry. The plan flags that such a job must still not end as executor-failed. The worker should check this against the close-out plan logic.
- The lane holds its semaphore during backoff, which can delay other commits on the same branch for up to about a minute.
