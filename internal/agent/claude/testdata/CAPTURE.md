# Capturing the real fixtures

`real_*.jsonl` (with `real_*.meta.json`) are scrubbed transcripts from the pinned
CLI (`TestedVersion`). The `synthetic_*` files stand in until a human commits
them. Four captures are required before #81 merges: `success`,
`error_max_turns`, `budget_exceeded` and `auth_bad_token`. `auth_none` and
`usage_limit_<scope>` are optional.

The harness is behind the `integration` and `capture` build tags, so `make test`
and the normal integration run never spend. It uses the adapter's own
`Command`, so the argv is exactly what ard runs. The repo each run works in
holds only fixed dummy text.

## Commands (on the Gate 1 Mac, on the PR branch)

```bash
claude --version                          # must equal TestedVersion; otherwise stop
export CLAUDE_CODE_OAUTH_TOKEN=...        # from `claude setup-token`; this shell only
unset ANTHROPIC_API_KEY
out=$(mktemp -d)                          # raw captures: never inside the repo
ARD_CAPTURE=success,max_turns,budget,auth_bad_token,auth_none ARD_CAPTURE_OUT="$out" \
  go test -tags 'integration capture' -run '^TestCapture$' -timeout 60m -v ./internal/agent/claude/
ARD_CAPTURE_OUT="$out" go test -tags 'integration capture' -run '^TestScrubCaptures$' -v ./internal/agent/claude/
go test ./internal/agent/claude/          # the real-fixture table and the scrub checks
less internal/agent/claude/testdata/real_*.jsonl   # read every line before committing
git rm internal/agent/claude/testdata/REAL_FIXTURES_PENDING \
       internal/agent/claude/testdata/synthetic_{success,error_max_turns,budget_exceeded,auth_failure}.jsonl
make lint test
git add internal/agent/claude/testdata/real_* && git commit -m "Add real stream-json captures"
rm -rf "$out"; unset CLAUDE_CODE_OAUTH_TOKEN
```

Cost: `success`, `max_turns` and `budget` are one small haiku run each and count
against the 5-hour window. `auth_bad_token` and `auth_none` cost nothing. The
bad-token case can take minutes (a 401 makes the CLI retry), so it has a
15-minute timeout and records the wall time and the number of retries in its
meta file. An expired token cannot be produced on demand; the bad token stands
in for it.

`TestScrubCaptures` replaces `session_id`, `uuid`, `cwd`, timestamps, ids and
every path of the run with stable placeholders, then fails closed if a UUID, a
home path, an email, `sk-ant-`, the user name or a credential value is left, or
if a second secret scan over your environment flags anything. It keeps
`resetsAt`, the `... resets ...` text, `api_error_status`, `terminal_reason`,
`usage`, `modelUsage`, `total_cost_usd`, `duration_ms` and `claude_code_version`,
because the tests assert on them.

`REAL_FIXTURES_PENDING` is the draft gate: `TestRealFixturesPresent` passes while
it exists, fails if it exists once all four are committed, and fails without it
while any is missing. The PR must not merge while it exists.

## The usage-limit capture (optional, never burn quota on purpose)

Until a real limit is captured, `synthetic_usage_limit.jsonl` stands in, modelled
on the documented format. A real capture answers what a `-p` run does at a
limit (exit or wait, the exit code, where the text appears, whether
`rate_limit_event` carries `resetsAt`). Two free routes:

- **While already limited.** When interactive Claude Code shows
  `You've hit your ... limit`, run this within the same window. Use the limited
  family's model for an Opus or Sonnet limit. The rejected request spends nothing.

  ```bash
  ARD_CAPTURE=usage_limit ARD_CAPTURE_MODEL=<opus|sonnet|haiku> ARD_CAPTURE_OUT="$out" \
    go test -tags 'integration capture' -run '^TestCapture$' -timeout 30m -v ./internal/agent/claude/
  ARD_CAPTURE_OUT="$out" go test -tags 'integration capture' -run '^TestScrubCaptures$' -v ./internal/agent/claude/
  ```

  The fixture is named `real_usage_limit_<session|weekly|opus|sonnet>.jsonl` from
  the scope the parser reads; the scrub step refuses a capture that shows no limit.

- **From an ard job.** After ard's first real limit stop, copy that job's
  `~/.ard/logs/.../<job>.stdout` (kept 7 days) to
  `"$out"/usage_limit.raw.jsonl`, write `usage_limit.meta.json` by hand
  (`{"case":"usage_limit","exit_code":<job exit code>,"seconds":0,"cli_version":"...","api_retries":0,"rate_limit_events":0,"result_subtype":"..."}`)
  and `usage_limit.env.json` (`{}`), then run the scrub step.

After it lands, note in `parse_test.go`'s header the exit code, the message text
and whether the run waited or exited, and delete `synthetic_usage_limit.jsonl`
if the real one covers it.
