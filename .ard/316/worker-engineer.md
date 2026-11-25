---
role: engineer
status: done
attempt: 1
---
Adds the three hidden pod-side commands of the Kubernetes executor, `job-pre`, `job-exec` and `job-post`, each in its own package and wired through `cli.Handlers` and `cmd/ard/main.go`. They follow the plan's pre/exec/post design, so a job pod can fetch its inputs, run the agent and serve the result.

## Changes
- `internal/gitx/local.go`: `CheckoutTree`, a `git checkout --detach --force` into a work tree with no `.git`. It keeps symlinks and exec bits and has no size cap. Test in `checkout_test.go`.
- `internal/cli`: `JobPre`, `JobExec` and `JobPost` handlers and opts types; new `cmd_job.go` with the three hidden commands (`--deadline` RFC 3339, `--git-base`, `--grace`, argv after `--`; bad input exits 2).
- `internal/jobpre`: reads the manifest and token from the Secret dir, fetches the base and role SHAs, history and pinned sidecar. Checks out the tree, exports the role and sidecar, scrubs and writes inputs, sets group permissions, and copies the binary to the shared bin dir. The token reaches git only through the environment. Permanent failures write the termination log and exit 3; others exit 1.
- `internal/jobexec`: child in its own process group, stdout teed to `/out/stdout`, SIGTERM then SIGKILL after the grace period, exit 124 on deadline, orphan reaping on Linux (only when PID 1), group-read on `/work`, done marker written last with O_TRUNC and O_NOFOLLOW. A command that fails to start still writes the marker (127).
- `internal/jobpost`: `GET /v1/result` only, key check, one-slot semaphore (429), 409 until the marker exists, then streams `tarball.Pack`. Shuts down on SIGTERM or the deadline and exits 0.
- `docs/layout.md`: three lines.

## Testing
Ran `go build ./...`, `go vet ./...`, `GOOS=linux go vet ./internal/jobexec`, and `go test` for gitx, cli, jobpre, jobexec, jobpost, docscheck, effects and cmd. All pass. A full `go test ./...` timed out at 2 minutes in this sandbox and was not completed.
New tests:
- `TestCheckoutTree`.
- `TestPre`: layout, read-only, history, missing SHA exits 3, symlink input exits 3, role repo mismatch, transient failure exits 1.
- `TestPreBadSecrets` and `TestPreTokenNotInArgv`.
- `TestExec*`: exit codes, tee, home, deadline, kill after grace, forwarded SIGTERM, grandchild, planted marker and modes, missing command, no argv.
- `TestPost` and `TestRunKeyAndPort`.
- `TestJobCommands`.

## Open issues
- The Authorization scheme for `job-post` is not defined in `jobpod`, so I used `Authorization: Bearer <key>`. #317's client must send the same.
- Orphan reaping runs only when job-exec is PID 1 and only on Linux, so the unit tests cannot cover it. The kind-based test in #321 is the real check.
- `job-exec` fails if `/job/home/.claude` already exists (a container restart). The pod should not restart this container.
