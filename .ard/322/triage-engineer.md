---
role: engineer
status: done
attempt: 1
---
Makes review-fix round-reason aware, adds a merger request path for conflict rounds through an optional Rounds dependency, and keeps production unchanged by leaving that dependency unset in servecmd. The plan has seven steps with tests for the switch off and on. Size M, risk low to medium.

## Open issues
- The switch is the merger's Rounds left nil in servecmd, not a new configuration key, so no protected file changes. Part B must set it.
- The cap reuses the existing counter, so conflict and review rounds share max_review_rounds; the issue's wording about counting them separately is read as tracking the reason separately in each round's event.
