# Plan for #205

## Implementation

Follows the owner's decisions in the issue comments: only gitleaks hits block (injected in code); a failed, timed-out or invalid aid lets the review run but it can only return `changes`; with no Dockerfile and no `ARD_AGENT_IMAGE` the aid runs on the host with an empty credential environment; the aid has no network; gitleaks is included. The change targets the local executor; k8s role images stay with #245.

### Steps

1. `internal/roles/roles.go`: change `ExecutableNames` to `[]string{"scan", "aid"}`. `internal/initcmd/plan.go:53` already marks listed base names `Exec`, so the starter commit writes `aid` as 100755. Check `internal/initcmd` tests that assert the executable set.
2. `internal/config/types.go`: add `AidPath string // Dir + "/aid"; "" = none` to `Role`, next to `ScanPath` (line ~125). `internal/config/discover.go` (the `switch kind`, `default:` branch near line 130, which rejects `scan` outside scanners): for `KindReviewer`, if `files["aid"]` exists it must pass `isExecutable`, else an `Error{Key: "aid", Msg: "must be executable (chmod +x)"}`; set `role.AidPath = dir + "/aid"` and `delete(extras, "aid")`. For every other non-scanner kind, `aid` is an error ("only reviewers may have an aid script"), like `scan`.
3. `internal/config/render.go` (`roleYAML`, ~line 168): add an `Aid` field filled from `role.AidPath`, so `ard config check` shows it. Update the golden or expected output in `internal/config` and `internal/configcmd` tests.
4. New `internal/reviewer/aid.go`, with these pieces:
   - `type aidResult struct { Lines []byte; Dropped int; Err string; Blocking []closeout.Finding }`.
   - `func (l *Loop) runAid(ctx, log, cfg, c candidate, p pinned, jobID string, budget time.Duration) aidResult`. It returns early with a zero result when `role.AidPath == ""`.
   - Inputs: `.ard-input/N/changed.txt`, the changed paths one per line (use `d.Git` for the name list between `p.base` and `p.tip`, skipping `.ard/N/`; add a method on gitx only if none exists, marked new), and `diff.patch`. Reuse the same `inputs` bytes the agent gets.
   - Job: `executor.Job` built via `agentjob.ExecJob`, then override `ID = sj.ID+"a"`, `Ref = p.tip`, `ReadOnly = true`, `Network = executor.NetworkNone` (enforced under Docker; the host ignores it), `Timeout = min(aidTimeout, remaining)`. `aidTimeout` is a new const of 5m and counts inside the review job's timeout. Use the same Dockerfile selection as `ExecJob`, so a role Dockerfile is used, else the default image, else the host.
   - Command: `executor.Command{Path: filepath.Join(h.View.Role, "aid")}` with no `Env` and no `Fixed`, so no model credential or GitHub token reaches it. Confirm the host executor's subprocess environment is the minimal allow-list and not the controller's own (read `internal/executor` host start code); if it is inherited, fix it in the executor.
   - Flow: `Prepare`, `Start`, `executor.WaitFor`, then read `h.StdoutPath` capped at 256 KiB (a longer stream is a failure "output over 256 KiB"), `Cleanup`.
   - A nonzero exit, a timeout or a Prepare/Start error becomes `Err` with a short reason. Never fail the review job.
5. Same file: `func validateAid(raw []byte, changed map[string]bool, sec redact.Scanner/Secrets) (clean []byte, dropped int, blocking []closeout.Finding)`.
   - Per line, decode with `json.Decoder.DisallowUnknownFields` into `struct{Tool, Rule, File string; Line int; Severity, Message string}`.
   - Drop and count: a bad JSON line, an unknown key, an empty `tool` or `rule`, a `file` that is absolute, contains `..` or a backslash, or is not in `changed` (use `internal/repopath` for the check), `line < 0`, a `severity` outside `critical|high|medium|low|info`, or a message over 1 KiB (truncate rather than drop).
   - Pass each message through `d.Secrets().Bytes` (as `testOutput` does) and the secret scanner. A line that still trips the scanner is dropped and counted.
   - For `tool == "gitleaks"` where `file:line` is an added line (`p.added`, via `gitx.LineRange`), emit a `closeout.Finding{Blocking: true, Severity: "high", Message: "[secret] gitleaks rule <rule>; see aid.jsonl"}`. It carries the rule id only, never matched text; the aid must not emit matched text, and `Secret`, `Match` and similar keys are unknown keys and get dropped.
6. `internal/reviewer/run.go`, after the claim label and `StartAttempt` (step 4) and before `d.Executor.Prepare` for the agent: call `runAid`.
   - On success append `executor.File{Path: dir+"/aid.jsonl", Data: header+clean}`, where the header is `agentjob.UntrustedHeader` plus a line giving the counts (kept, dropped), and add the path to `untrusted`. When the aid is absent, write nothing; the prompt falls back to running tools.
   - On failure write `aid.jsonl` as the header plus "The aid failed: <reason>. No tool results are available." and add it to `untrusted`.
   - Set `rc.AidFailed = reason` and `rc.Injected = blocking` on the `ReviewContext` (step 3 of `run`; the struct is built before this, so build the aid first or assign afterwards, before `finish`).
   - `rc` is persisted by FinishWith, so the Reaper path keeps both.
   - The agent inputs must be added to `inputs` before `ej` is built, so move the `Inputs` assignment accordingly.
7. `internal/closeout/review.go` and `prepare.go`.
   - Add to `ReviewContext`: `AidFailed string` and `Injected []Finding` (both JSON-persisted, unlike `Added`).
   - In `prepare.go` (review branch near line 236, and the downgrade at line ~131): append `Injected` to the findings (dedupe on file, line and a `[secret]` prefix); if `AidFailed != ""` or `HasBlocking(all)`, force the verdict to `changes` and set `Downgraded` or a new `AidNote` string.
   - `RenderReview` adds one note line when `AidFailed` is set ("The pre-review aid failed (<reason>), so this review cannot approve") and marks the injected findings as added by ard.
   - The verdict is forced only when the role has an aid, so roles without an aid behave exactly as before. Scope this to `rc.AidPath != ""` (a bool `HasAid` on `ReviewContext`).
8. `internal/roles/reviewers/security-review/aid` (new, executable `#!/bin/sh`, `set -eu`). It reads `$ARD_WORK/.ard-input/*/changed.txt` (get the work path from the `ARD_WORK` or `h.View.Work` env the executor exports; confirm the name in `internal/executor`). It runs gitleaks (`detect --no-git --redact --report-format json`, over the changed files only), gosec (`-nosec`, `-fmt json`, on changed Go packages) and semgrep (`--metrics=off --disable-nosem --config /opt/aid/rules`, offline). Each tool's output is normalised to the JSONL contract (`tool`, `rule`, `file`, `line`, `severity`, `message`), with `jq` as in the lint scanner's `normalize.jq` (new `normalize.jq` beside it). It never prints matched text. It never executes repository code (no `go build`, no `go vet` that runs generators; gosec works on parsed source). A tool that is missing exits nonzero with a message on stderr only.
9. `internal/roles/reviewers/security-review/Dockerfile` (new) on the #55 base image (`ARD_AGENT_IMAGE` build in the #243 files; read how the other Dockerfiles pin `GO_IMAGE` and mirror them). Pin gosec, gitleaks and semgrep by version and sha256 (binary release checksums) the way `scanners/lint/Dockerfile` pins jq. Vendor a small semgrep ruleset under `internal/roles/reviewers/security-review/rules/` (new files) and `COPY` it to `/opt/aid/rules`. The worker has no network to fetch checksums: write the pins that can be taken from the existing Dockerfiles, and mark each other digest with a `# Verification pending human:` comment in the header, as the lint Dockerfile does. Say so in the PR body.
10. `internal/roles/reviewers/security-review/prompt.txt`: add `.ard-input/N/aid.jsonl` to the input list (untrusted). The prompt reads it first, treats each line as a lead to confirm or reject, and runs gitleaks, gosec and semgrep itself only when the file is absent. If the file says the aid failed, it states that in `## Checks`. Update the "run when installed" paragraph accordingly. `internal/roles/roles.go` embeds `reviewers` already, so no embed change.
11. `docs/proposal.md`: Reviewer loop step 3 (the aid step, inputs, limits, no credentials, `--network none`, the 5m timeout inside the job's timeout, the failure rule, the gitleaks blocking rule); the security-review paragraph (replace "Running pinned tools in controller code is #205"); the reviewer table row; the Configuration role-directory tree (line ~435) gaining `reviewers/<name>/aid (executable, optional)`; and the host-mode residual risk next to #84's: an aid run on the host reads PR code and runs ard-owned code on it without a sandbox and with no network enforcement. `internal/docscheck` may check the doc against code, so run it. Update `docs/TODO.md` if it lists #205.

### Test plan

Test command: `make lint test` (`make test` runs `go test ./...`). Docker test: `go test -tags integration ./internal/roles/ -run SecurityReviewAid`, which needs Docker and the built image.

- `internal/config/discover_test.go` (or `load_test.go`)
  - `reviewer_with_executable_aid`: `Role.AidPath` is `reviewers/x/aid`.
  - `reviewer_aid_not_executable`: error with key `aid`.
  - `scanner_with_aid`, `worker_with_aid`: error.
  - `reviewer_without_aid`: `AidPath == ""`.
- `internal/configcmd` or `config/render_test.go`: `ard config check` output shows the aid path.
- `internal/initcmd` and `internal/roles` tests: `aid` is in `ExecutableNames`, and the security-review plan entry has `Exec: true`; the existing `security_review_test.go` checks for the new `aid`, `Dockerfile` and prompt lines.
- `internal/reviewer/aid_test.go` with the fake executor (follow `internal/reviewer` loop tests)
  - `aid_output_reaches_agent`: the aid prints 2 valid lines; the agent job's `Inputs` hold `aid.jsonl` with both and its path is in the untrusted list.
  - `aid_job_shape`: `ReadOnly`, `Network == NetworkNone`, no `Env`, no `Fixed`, `Ref` is the pinned tip, timeout is 5m or the remaining time if smaller.
  - `invalid_lines_dropped_and_counted`: unknown key, `file: "../x"`, an absolute path, a file not in changed.txt, bad JSON, a bad severity; the header says N dropped and none appear in the output.
  - `output_over_256KiB`: counted as failure.
  - `secret_in_message_is_redacted`: a token in `message` never reaches `aid.jsonl`.
  - `aid_nonzero_exit`, `aid_timeout`, `aid_prepare_error`: the review still runs; `aid.jsonl` says the aid failed and why; the finish call receives `AidFailed`.
  - `no_aid_no_file`: a role with no `AidPath` writes no `aid.jsonl`, and behaves as before.
  - `gitleaks_on_added_line_blocks`: a finding in `Injected` with `Blocking` and `high`; a gitleaks hit on a non-added line or a gosec or semgrep hit does not inject.
- `internal/closeout` tests
  - `injected_finding_forces_changes`: the agent verdict is approve and omits the finding; the plan has verdict `changes` and the finding is listed.
  - `aid_failed_cannot_approve`: approve becomes `changes` with a note.
  - `no_aid_unchanged`: existing review tests still pass.
  - A `ReviewContext` JSON round-trip keeps `AidFailed` and `Injected`.
- `internal/roles` integration test (tag `integration`, Docker): builds the #43 S1, S4 and S5 fixtures at run time (follow `lint_docker_integration_test.go`), runs `aid` in the built image with `--network none`, and expects gitleaks, gosec and semgrep findings for the three fixtures with valid JSONL. A second case asserts `aid` prints no matched secret text.
- Edge cases: an empty `changed.txt`; a path with a space or a non-UTF-8 byte; a very long line; a duplicate finding.

### Risk

High: security-sensitive and it touches several packages.
- The aid reads untrusted PR code. In Docker it runs read-only with no network and no credentials. On the host (owner-decided) it has no sandbox, so the host-mode residual risk is documented next to #84's. The aid must only parse code, never run it.
- Config discovery and `ard config check` output change, and `closeout.ReviewContext` is persisted in the close-out record, so old records must still decode (the new fields are optional).
- Redaction and the secret scan must cover the aid output before the agent sees it.
- Blocking logic changes how reviews verdict: it applies only to roles with an aid.
- Protected paths: no edit is needed under `.ard/reviewers/`, `.ard/config.yaml` or `.github/`. Copying the starter aid into this repository's own `.ard/reviewers/` is a protected-path change and is out of scope; a human does it.
- The pinned tool digests need a human to verify, because the worker has no network.

### Size

L (roughly 1,200 to 1,800 changed lines including tests, the rules, the Dockerfile and docs); under the 3,000-line block limit, and may cross the 2,000-line warn level, in which case it carries a large-change note.

Files touched: `internal/roles/roles.go`, `internal/config/types.go`, `internal/config/discover.go`, `internal/config/render.go`, `internal/reviewer/run.go`, `internal/reviewer/aid.go` (new), `internal/closeout/review.go`, `internal/closeout/prepare.go`, possibly `internal/gitx` (changed-paths helper) and `internal/executor` (environment check), `internal/roles/reviewers/security-review/{aid,normalize.jq,Dockerfile,prompt.txt,rules/*}` (aid, normalize.jq, Dockerfile and rules new), `docs/proposal.md`, the tests listed above.
