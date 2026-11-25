---
role: engineer
status: done
attempt: 1
---
Switches the network inputs back-route on: an engineer `done` in a network round re-arms the lead, the lead re-opens the other triage roles, the gate holds for the engineer's declared inputs, and a network round always needs a fresh approval.

## Changes
- `internal/closeout`: round detection now keys off the plan from the prepared phase on (fail-closed after a crash); new `NetworkRoundOpen`, `NetworkRoundCount`, `LeadRerun`, `InputsOf`; `NetworkState` and `Plan` gain `Round`/`Done`/`Rerun`, `Rearmed`, `Clear`, `Inputs` (all omitempty, no version bump). `Prepare` persists the round state; `Build` stays pure. `request` removes `Plan.Clear` labels (404 is success) before adding requests. `inputs:` frontmatter (triage engineer only, max 10, https URLs) is taken from the redacted parse; an unreadable list ends `needs-human`.
- `internal/triage`: new `inputs.go` (path check, `decideInputs`, per-tick main SHA, SHA-keyed entry cache, waiting comment); `fanin.go` gates inputs and uses `rounds > 0` to force approval, with a `round=<n>` marker suffix; new `Deps.MainSHA`/`MainEntry`.
- `internal/gitx/local.go`: `EntryAt` (ls-tree entry, no content). `internal/servecmd`: wires `Network` into the closer (bounce on) and the two main readers.
- Engineer prompt starter and `docs/proposal.md` document `inputs:`, the re-run and the approval rules.

## Testing
`go test` on closeout, triage, gitx, servecmd, docscheck and roles pass; `gofmt` clean. `go vet ./...` only reports an existing testdata file that does not parse. Added tests for the re-arm and lead re-run labels, the round-open and `LeadRerun` logic (including an unclosed worker record), Clear ordering with a 404, the `inputs:` parser, path checks, `decideInputs` (symlink, submodule, size, worker without network), and the gate holding, posting once, then asking for approval with `require_approval: false`. I changed the old rounds test, which asserted that an unclosed record does not count.

## Open issues
- Not done: the status issue's "waiting for inputs" line (the renderer was not located) and the intake test for a re-armed lead being re-claimed. No separate Resume or servecmd test was added.
- The main fetch for inputs is once per tick, but `planExists` still fetches per issue.
- The live prompt under `.ard/triage/` is protected; a human must update it to match the starter copy.
