---
role: engineer
status: done
attempt: 1
---
Plans the network inputs back-route part 3 in ten steps: persisted round state in the close-out record, the lead re-arm and re-run list, the `inputs:` frontmatter and its gate reading from main, a per-round approval marker, and switching the bounce on in serve. Size L, risk high because it changes the label state machine and stored job state.

## Open issues
- A maintainer must do the requested architect review of the persisted round state and lead re-arm by hand, since no architect role exists here.
- The live engineer prompt under .ard/triage/ is a protected path, so a human must update it to match the starter copy that the plan edits.
- The plan leaves the status issue's "waiting for inputs" rendering to be located by the worker, as the exact renderer was not pinned down.
