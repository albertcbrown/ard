---
role: engineer
status: done
attempt: 1
---
Records Claude subscription usage-window readings (5-hour, weekly) from the agent stream and logs them as `limits.reading`, without gating any job. Readings are taken only from agent and fix steps, never from a test step, per the plan's owner amendment.

## Changes
- `internal/agent/agent.go`: `Window` type and `Result.Windows`.
- `internal/agent/claude/parse.go`: reads `rate_limit_info.unifiedWindows` (object keyed by window, or array with a type field) from every `rate_limit_event`; kept raw so a malformed value never drops the event; last reading per type wins, sorted; set also with `ErrNoResult`.
- `internal/limits`: `Reading`, `Book.Observe`, `Book.Readings` (new `readings.go`), persisted under meta key `limits.readings`; `Open` loads it. Unchanged readings are skipped, so a step seen twice is neither logged nor written twice.
- `internal/reaper`: `ReadingRecorder` interface; `Finisher.FinishWith` records `res.Windows` only when the handle is an agent or fix step.
- `internal/worker`: `Deps.Readings`; records after each agent/fix step.
- `internal/servecmd/observe.go`: `readingObserver` records for OAuth credentials only; errors logged as `limits.observe_failed`, never failing the job. Wired into the Finisher and worker deps. `parseFunc()` is untouched.
- `docs/proposal.md`: adapter paragraph updated.
- Fixture `synthetic_windows.jsonl` and tests.

## Testing
`go vet ./...` and `go test ./...` pass. Added: parser tests (windows, last-wins, malformed, no-result, millis), limits readings tests, `TestReadingsIgnoredOnTestStep` (reaper), a worker loop test that only agent/fix steps record, and servecmd observer tests (OAuth recorded, API key not, error swallowed).

## Open issues
- The `unifiedWindows` payload shape and utilization scale are assumed (no real capture exists); the parser is tolerant and stores the value as reported. A human should check a real capture.
- Following the owner amendment, the plan's `observing` ParseFunc wrapper was replaced by a recorder called at the Finisher and worker step sites.
- Ticking the spike box on #63 and watching a few days of real runs need a person.
