---
role: engineer
status: done
attempt: 1
---
Fixes the data race in TestRunWithUI by synchronising through a channel and an atomic flag instead of sleeps, and adds a `make test-race` target.

## Changes
- internal/servecmd/uilog_test.go: the fake `newProgram` sends the created program on a buffered channel; the test receives it before `Send`. `stopped` is now an `atomic.Bool`. Both ordering sleeps and the `time` import are removed. The SIGINT scenario cancels the context before starting `runWithUI`.
- Makefile: new `test-race` target (`go test -race ./...`), added to `.PHONY`.
- `runWithUI` is unchanged; the race detector found no race in it.

## Testing
- `go test -race -count=200 -run TestRunWithUI ./internal/servecmd/`: pass.
- `go test -race ./internal/servecmd/ ./internal/ui/`: pass.
- `make lint test`: pass.

## Open issues
- Running `-race` in CI needs a change under .github/, which agents cannot make; a maintainer would have to add it.
