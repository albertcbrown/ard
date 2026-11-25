# Plan for #441

## Implementation

### Steps
1. `internal/servecmd/uilog_test.go`, `TestRunWithUI` (line 93): replace `var fp *fakeProgram` with `progs := make(chan *fakeProgram, 2)`. In the fake `newProgram`, build the `fakeProgram`, send it on `progs` (buffered so the first scenario never blocks) and return it. Reason: removes the unsynchronised shared `fp`.
2. Same test: replace `stopped := false` with `var stopped atomic.Bool` (import `sync/atomic`); the run funcs call `stopped.Store(true)`, the checks use `stopped.Load()`. In the second scenario use `stopped.Store(false)` before starting, or a second variable. Reason: the run func's goroutine writes it.
3. SIGINT scenario: drop `time.Sleep(20ms)` and call `cancel()` directly before `runWithUI` (cancel-before-start is valid: the watcher goroutine sees `ctx.Done()` and sends Quit; `Run` returns when quit closes). Drain `<-progs` afterwards so the channel is empty for scenario two.
4. UI quit scenario: drop the sleep; `fp := <-progs` (blocks until `newProgram` ran), then `fp.Send(tea.Quit())`, then `<-done`. Reason: channel receive is the ordering.
5. Remove the `time` import if now unused (check with the build).
6. `internal/servecmd/ui.go` `runWithUI`: read-only check under `-race`. By inspection `p` is assigned before the watcher goroutine starts, so no change is expected. Only edit it if the race detector reports a race there.
7. `Makefile`: add `test-race` (`go test -race ./...`) and add it to `.PHONY`. Leave `test` as is. Do not touch `.github/` (protected).

### Test plan
- `go test -race -count=200 -run TestRunWithUI ./internal/servecmd/` passes (the issue's acceptance command).
- `go test -race ./internal/servecmd/ ./internal/ui/` passes.
- `make lint test` passes; `make test-race` runs (new).
- Edge cases: the cancel-before-start path, and the quit path where `Send` is called as soon as the program is received.

### Risk
Low. Test-only change, plus a Makefile target. No protected paths are touched. `runWithUI` changes only if the race detector finds a real race, which inspection says it won't. Adding `-race` to CI needs a maintainer, because `.github/` is protected.

### Size
S. Files: `internal/servecmd/uilog_test.go`, `Makefile`. `internal/servecmd/ui.go` only if a race is found.
