---
role: engineer
status: done
attempt: 1
---
Adds a read-only Bubble Tea terminal UI (the command center) to `ard serve`. It draws the in-process Snapshot once a second, sends logs to a capped serve.log while it runs, and leaves today's output unchanged without a terminal.

## Changes
- `go.mod`/`go.sum`: `charm.land/bubbletea/v2` v2.1.1 and `charm.land/lipgloss/v2` v2.1.0 (module paths checked), plus `charmbracelet/x/ansi` v0.11.9 (kept at the older release on purpose). CGO stays off; `CGO_ENABLED=0 go build ./...` passes.
- `internal/ui` (new): `Clean`/`Trunc` sanitiser, `Model` (tick, snapshot, resize), keys (`?`, `j`/`k`/arrows, `o`, `q` with confirm, Ctrl-C), pure `Render` (header, needs you, running, waiting, limits, paged issues, footer; next tick and recent events on large terminals), golden frames.
- `internal/servecmd`: `tty.go` (`uiWanted`), `uilog.go` (serve.log, 0600/0700, rotates to `.1` at 10 MiB), `ui.go` (`runWithUI` runs the controller next to the UI; quit or ctx cancel stops the controller and waits for it), wiring in `serve.go` (logger handler and config-loader stderr go to the file, `logs: <path>` printed on exit).
- `internal/cli`: `--ui` / `--no-ui` (mutually exclusive, exit 2) mapped to `ServeOpts.UI`.
- `docs/proposal.md`: Command center paragraph under Observability.

## Testing
`make lint test`: all pass. New tests: sanitiser, golden frames (80x24, 160x50, empty, tiny), escape-inert rendering, key handling, tick/resize with a counting fake `take`, `uiWanted` table, serve.log mode and rotation, `runWithUI` with a fake program (ctx cancel and UI quit both stop the controller), CLI flags.

## Open issues
- #407 was not readable here, so panel wording follows the issue slice and the Snapshot fields. `IssueView` has no title, so the issues panel shows number, state, jobs and cost.
- Child process output already goes to per-job log files in the executor, so only the controller's own stderr and slog output are redirected.
- `go vet` prints a pre-existing parse note for a roles testdata file; unrelated.
