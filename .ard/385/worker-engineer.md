---
role: engineer
status: done
attempt: 1
---
Adds the network bounce to the triage engineer in close-out: a first-job needs-network worker is re-requested to the triage engineer instead of ending needs-human. It is switched off (the new dependency is unset in servecmd), so live behaviour stays part 1's needs-human.

## Changes
- `internal/closeout/network.go` (new): `NetworkRound` (first job only, nothing pushed, one round, engineer role needed), `NetworkRounds` (loop guard count from closed worker close-outs), `NetworkRequest` and `RenderNetworkRequest` (rendered from the record's `Plan.Network` only, untrusted header, one line per field).
- `plan.go`, `worker.go`, `closeout.go`: `Plan.Bounced`, `Job.Network`, `WorkerResult.Pushed`, `Deps.Network` (nil = off). A bounce requests `ard:triage:engineer` and removes `:triage:engineer:done`, `ard:worker:<role>`, then `:running`; a not-found on removal counts as success for a bounce. Non-bounce cases keep needs-human and record the reason.
- `prepare.go`: with `Deps.Network` set, counts prior rounds for the guard. `comment.go`: fixed bounce line.
- `internal/worker/run.go`: sets `Pushed` on the result. `internal/triage/run.go`: adds `network-request.md` as an untrusted input when a bounced record exists.
- Starter prompts under `internal/roles/` and their pins; `docs/proposal.md`; a comment in `servecmd/reaping.go`.

## Testing
Ran `go test` for closeout, triage, worker, roles, servecmd and reaper: all pass. Added Build cases (bounce, Reaper, not pushed, branch, PR, pushed, second round, no engineer role, off), rounds/request/render tests, closer tests (label order, resume after a failed removal, not-found vs other error, loop guard, off), a triage input test (bounced, not bounced, none) and a `Pushed` assertion in the worker loop test. I did not run `make lint`.

## Open issues
- The servecmd "unset as shipped" check is a code comment only; the closer's deps are private there, and the existing needs-network tests still pass.
- The live `.ard/` prompts are protected; a human PR must follow.
- The loop guard counts every closed worker needs-network record, including ones from before the bounce existed.
