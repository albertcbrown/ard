---
role: engineer
status: needs-human
attempt: 1
---
The change is size XL, so no plan was written. It touches config parsing and `config check`, the executor (docker argv, env and the host warning), the claude user message, triage's fixed line, the worker's test step and PR body, six starter prompts with their tests, the proposal and the chart. Together with tests that is likely over the worker's 2,000-line block limit, and the risk is medium because it changes documented security behaviour.

## Open issues
- Proposed split, each part its own issue that builds and tests on its own:
  1. Config and plumbing: a `network` key on every stage (default true) in `internal/config` (`types.go`, `raw.go`, `load.go`, `render.go`) and `internal/configcmd`, with `false (advisory)` output, the host-mode `false (advisory, host)` output and the host × untrusted × `network: true` warning that names the App key. Also a new `Spec.Network` in `internal/agent/agent.go`, the "Network: available/unavailable." line in `userMessage` (`internal/agent/claude/prompt.go`), `ARD_NETWORK` in `ardEnv` (`internal/executor/local/env.go`), and the claim-time and `warnHostJob` warnings.
  2. Docker enforcement for the worker test step: a way to carry the network setting on the executor job or command, `--network none` in `runArgv` (`internal/executor/local/docker.go`), and the call site in `internal/worker/run.go` (the test step near line 630). This depends on a decision about how the setting reaches `runArgv`.
  3. Triage's fixed line listing each worker role's setting, in `internal/triage/run.go` and the user message.
  4. Starter prompts in `internal/roles/` (worker, lead, engineer, eng-review, security-review) with prompt-content tests.
  5. The worker PR body's Fetched inputs list in `internal/worker/pr.go`, plus the proposal and chart-comment fixes.
- Design gaps a maintainer should settle first:
  - The capture note has no defined location or format, yet the PR body's Fetched inputs list depends on parsing it.
  - The chart comment is under `deploy/`, a protected path, so that part waits for human approval.
  - The live `.ard/` prompt copies stay out of scope, as the issue says.
