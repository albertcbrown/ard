---
role: engineer
status: needs-human
attempt: 1
---
The request is size XL: the four slices the issue proposes come to roughly 4,000 changed lines, over the worker's block limit, and it adds a new TUI dependency and new flags. No plan was written; the split below is the proposal.

## Open issues
- Split into four sub-issues, in order: (1) the Snapshot model in internal/pool, internal/limits and internal/servecmd, with limited and budget refusal counts, pause state, tick times and an events ring buffer, with no UI; (2) the read-only TUI, with TTY detection, `--ui` and `--no-ui`, log redirect, sanitising, resize, help, quit, selection and open; (3) the pause key and the slot keys, including the reload fix at internal/servecmd/serve.go:279 (`p.SetMax(c.MaxPods)`) and the proposal update; (4) the approval key through the operator's `gh`, with a new effect-layer method, dry-run and the browser fallback.
- Each sub-issue can then get its own plan; slices 2 to 4 depend on slice 1.
- A maintainer should choose the pure-Go TUI library or a small custom renderer (this is a product and design decision).
- The `.ard/config.yaml` file stays untouched, as the issue says; the slot override is runtime only.
