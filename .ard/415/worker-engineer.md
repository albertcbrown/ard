---
role: engineer
status: done
attempt: 1
---
Adds the `p`, `+` and `-` keys to the serve command center: `p` toggles the local pause file, and `+`/`-` set a bounded runtime slot override. The config reload no longer wipes the override. The implementation is already in the branch's earlier commit for this issue, and this round changes no code.

## Changes
- internal/servecmd: slot override kept across config reloads until `max_pods` on main changes (logs `pool.max_override`), plus the pause and slot controls wired into serve.
- internal/snapshot, internal/ui: pool view carries the config cap and override flag; keys, toasts, confirm-to-unpause, header `max_pods N (config M)`, draining marker, and help text on dry-run behaviour.
- docs/proposal.md: Concurrency (runtime override) and kill-switch (UI key) paragraphs.

## Testing
The previous `make lint test` run failed only in `TestTransientCancelledInBackoff` in internal/committer, where a git subprocess was killed. It is unrelated to this change. Rerunning internal/committer, servecmd, ui, snapshot and pool passed.

## Open issues
- `TestRunWithUI` shows a data race under `-race` that predates this change and is unrelated to it.
- The change uses `WithControls` instead of changing the `ui.New` signature as the plan proposed.
