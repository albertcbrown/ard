---
role: engineer
status: done
attempt: 1
---
Adds a pause toggle and slot override keys to the serve command center, backed by a small override type that the config reload respects until the max_pods value on main changes. The plan has seven steps, with tests that use a fake clock and config reloads. Size M to L, risk medium.

## Open issues
- The plan step that wakes the loops after unpause should check how the controller's wake call treats an empty name list; the plan uses the selected loop names to avoid it.
- The changed `ui.New` signature is internal only, but every caller and test needs updating.
