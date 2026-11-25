---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Slice 4 of the serve command center adds an approve key to the terminal UI. It shells out to the operator's own gh CLI through a new effect-layer method, so dry-run records the call in the ledger instead of running it, and a successful approval wakes the triage loop. The read-only TUI this slice depends on has already landed on main (merged as the part 2 pull request), so the work can start now. The engineer is the only triage role configured, so it plans the steps directly.

## Routing
- triage:engineer: plan the confirm screen in internal/ui, the new effects method and its dry-run ledger entry, the gh-missing fallback to the issue URL, the triage wake-up, the proposal note and the tests with a fake gh on PATH.
- worker engineer: Go change across the UI, effects and serve wiring, with unit tests, run with make lint test.

## Open issues
- The slice touches several packages (ui, effects, servecmd, triage wake-up) and adds an operator-facing key. No architect, product or documentation triage roles are configured, so the reviewed design in the parent issue is the only design input.
- Approval runs the operator's gh credentials from inside ard serve. Reviewers should check that the App client never adds the approval label and that the subprocess arguments use only the sanitised issue number and the configured repository.
