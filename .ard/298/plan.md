# Plan for #298

## Implementation

### Steps
1. `internal/executor/local/docker.go`, `runArgv` (after the `--label` block, before the volume mounts): add `if h.Network == executor.NetworkNone { a = append(a, "--network", "none") }`. Advisory and the zero value add nothing. Reason: this is the only place Docker enforcement happens. `checkNetwork` in `env.go` already refuses a NetworkNone job that carries a credential, so an agent step can never get it by mistake.
2. `internal/worker/run.go`, `(*job).execJob` (~317): after building `ej`, add `if !role.Network { ej.Network = executor.NetworkAdvisory }`. Reason: the worker builds its own Jobs and does not use `agentjob.ExecJob`, so its agent steps do not carry the advisory marker today. The issue requires agent-step Jobs to carry `NetworkAdvisory`.
3. `internal/worker/run.go`, `testLoop` (~632): right after `ej := jb.execJob(...)` add `if !jb.role.Network { ej.Network = executor.NetworkNone }`. Reason: the test step has no model credential, so it can run with `--network none`. Host mode and k8s cannot enforce it; the host warning from #297 still applies.
4. Wording: `internal/config/types.go:133` comment becomes "default true; false is enforced for the worker test step in Docker, advisory elsewhere". In `internal/config/render.go`, update the comment (~95) and `networkText` (~208) only if the output stays accurate. The simplest option is to leave the rendered strings alone, since agent steps are still advisory, and fix only the comments. If the strings change, update `internal/configcmd/config_test.go:411` to match.

### Test plan
Test command: `go test ./internal/executor/... ./internal/worker/... ./internal/config/... ./internal/configcmd/...` (then `make test` if that is the repo target).
- `internal/executor/local/misc_test.go`, `TestRunArgv` table: add case "network none" (`h.Network = NetworkNone`), which has `--network none`. Add cases "network advisory" and "network on (zero value)" asserting no `--network` (put `--network` in `not`; the check pads with spaces, so it is a whole-word match). Also check that `none` appears right after `--network`.
- `internal/worker/loop_test.go` (using `fakeEx` in `harness_test.go`; record the Jobs passed to `Prepare` if it does not already): with a `network: false` worker role, the test-step Job (id kind `t`, see `kindOf`) has `Network == NetworkNone` and every agent-step Job has `NetworkAdvisory`. With `network: true` all Jobs have `""`.
- Edge case: a test-fix loop (`max_test_fixes` > 0) runs `t1`, a fix step, then `t2`. Both test steps carry `NetworkNone` and the fix step carries `NetworkAdvisory`.
- Failure case: the existing `checkNetwork` test still passes, so a credentialed command under NetworkNone is rejected.

### Risk
Low to medium. The change is small, but it can break a role whose `tests` command needs the network (for example a dependency download) once `network: false` is set. That is the intended behaviour, but it should be mentioned in the PR. No protected paths are touched (`internal/` only) and there are no migrations. The enforcement is a Docker flag only; the host executor is unchanged.

### Size
S. Files: `internal/executor/local/docker.go`, `internal/worker/run.go`, `internal/config/types.go`, possibly `internal/config/render.go`, plus tests in `internal/executor/local/misc_test.go` and `internal/worker/loop_test.go`. About 60 to 120 changed lines in total.
