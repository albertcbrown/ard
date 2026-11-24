# Plan for #300

## Implementation

### Steps
1. `internal/closeout/rolefile.go`: add `const MaxFetched = 50` (new) and `MaxFetchedURL = 2000` (new). Add `type Fetched struct { URL, Date string; Status int; SHA256, VerifiedBy, Path string }` (new). Add `Fetched []Fetched` to `RoleFile`. Add `rawFetched` (pointer fields: `url`, `date`, `status`, `sha256`, `verified_by`, `path`) and `Fetched *[]rawFetched \`yaml:"fetched"\`` on `rawFront` (KnownFields(true) already rejects unknown keys inside entries). Reason: strict parse like findings.
2. Same file: add `fetchedOf(raw []rawFetched) ([]Fetched, error)`, modelled on `findingsOf`:
   - more than `MaxFetched` entries is malformed;
   - `url` required, `https://` only, parsed with `net/url`, non-empty host, no userinfo, no whitespace, control chars, backticks or `<>`, at most `MaxFetchedURL` bytes;
   - `date` required, must parse with `time.Parse("2006-01-02")`;
   - `status` required, 100..599;
   - `sha256` required, exactly 64 lowercase hex chars;
   - `verified_by` required: either the literal `none` or an https URL passing the same URL check;
   - `path` required, passes the existing `checkFindingPath` (clean repo-relative slash path).
   Errors read `fetched %d: <field> ...`. Call it from `Parse` next to the findings call.
3. Same file, `checkTypes`: add a `case "fetched"` with a new `fetchedTypes(v)` (sequence of mappings; `url`, `date`, `sha256`, `verified_by`, `path` must be `!!str`, `status` must be `!!int`). The `date` value `2026-11-23` is tagged `!!timestamp` by yaml.v3, so accept `!!timestamp` or `!!str` for `date` and read it as text through the raw field (declare `Date *string`; verify yaml.v3 decodes a bare date into a string field, and if not, decode through `yaml.Node.Value`). An all-digit `sha256` would be tagged `!!int`; the `!!str` check rejects it, which is correct.
4. Same file, `check(j Job)`: add `if len(rf.Fetched) > 0 && j.Stage != StageWorker { return malformed("fetched is allowed only on a worker") }`.
5. `internal/closeout/worker.go`: add `Fetched []Fetched` to `WorkerResult`, documented as parsed from the worker's role file and never from free text. `buildWorker` needs no change.
6. `internal/worker/run.go`: add `fetched []closeout.Fetched` to `job`. In `round`, after `jb.setSummary(...)`, call new `jb.setFetched(ctx, s.h, rf.Fetched)`. It applies the same redaction as `setSummary` (`d.Secrets().WithPaths(...)`, then `sec.String` and `d.Scanner.Scan` over the rendered text); on any hit or error it drops the list and logs `worker.fetched_withheld`. Where `finishWithPR` builds the result (near line 797, next to `res.Large = jb.large`), set `res.Fetched = jb.fetched`.
7. `internal/worker/pr.go`: in `Body`, after the Tests and Large lines and before Files, when `len(b.Result.Fetched) > 0` write `\n**Fetched inputs:**\n` followed by one bullet per entry: `- <url> → <path> · sha256 <sha> · verified by <verified_by or "none (unverified)">`. Values are rendered in code spans (validation forbids backticks and whitespace), so no `QuoteSummary` and no free text. When the list is empty the section is absent. Keep the existing `Body` output byte-identical in that case.
8. `docs/proposal.md`:
   - `.ard/` contract (role file frontmatter, near the worker role file text, ~line 335): document `fetched:` (fields, https only, at most 50, worker stage only, invalid entry fails the parse like a finding) and that the PR body list comes from it.
   - Line ~283: replace "agent jobs get only their model credential" with the real position: an agent job's environment holds its model credential, and with `network: true` (the default) it may reach the network. The per-role `network` setting is advisory.
   - Line ~335: replace "uses no other network" the same way: the worker may fetch inputs, records each one under `fetched:`, and treats fetched bytes as untrusted data.
   - Line ~490 (the reported line ~444 does not match any egress text in the current file): correct "limits egress to the model API and package registries" so it states that no NetworkPolicy enforces egress in this part (the chart comment is #296, a human PR).
   - In the `network` paragraph (~476): state what `true` and `false` mean per executor as the code does today (host: `ARD_NETWORK` and prompt line only, warning logged; Docker: check `internal/executor/local/docker.go` for what is enforced at the worker's test step and say exactly that); list the seven risks from #276, the residual risk, that fetched bytes are untrusted data, and the recommendation of a workspace API key with a spend cap for roles with network.
   Before writing, read `internal/executor/local/*.go` and `internal/worker/run.go` (around line 240) so the text matches landed behaviour, not the plan in #276.
9. CAPTURE.md: this change adds no `CAPTURE.md` file. `internal/roles/testdata/lts/api/` has none today. The proposal states the convention (a `CAPTURE.md` table with the same fields plus the regen command beside each fixture set) and nothing more.

### Test plan
Command: `make test` (runs `go test ./...`); focused: `go test ./internal/closeout ./internal/worker`.
- `internal/closeout/rolefile_test.go`, table `TestParseFetched`:
  - valid single entry with `verified_by` URL: parses all six fields;
  - `verified_by: none`: accepted;
  - no `fetched:` key: `Fetched` nil;
  - 50 entries accepted, 51 rejected;
  - rejected: `http://` URL, `ftp://` URL, URL with userinfo, empty host, URL with a space or backtick; missing url, date, status, sha256, verified_by, path;
  - rejected: sha256 of 63 chars, 65 chars, non-hex, uppercase; bad date (`2026-13-40`, `yesterday`); status 99 and 600; `verified_by` that is `http://` or not a URL; path absolute, with `..`, with a backslash, empty;
  - rejected: unknown key inside an entry; `fetched:` not a list; `status: "200"` (string);
  - each rejected case is `ErrMalformed` and the message names the entry number.
- `internal/closeout/rolefile_test.go` `check`: `fetched` on a review or triage role file is malformed; on a worker job it passes.
- `internal/worker/unit_test.go`: `TestBodyFetched` (two entries produce the Fetched inputs list with URL, path, sha256 and verified_by, "none (unverified)" for `none`) and extend `TestBody` to assert the section is absent when empty. Case with markdown-looking text in a path is impossible after validation, so assert that a rendered line never contains free text from the summary.
- `internal/worker/loop_test.go` (or harness test): a role file with `fetched:` in the worker run reaches the PR body; a secret-shaped URL query withholds the list.
- Doc check: grep `docs/proposal.md` for "only their model credential" and "uses no other network" and expect no hits.

### Risk
Low to medium. The parser is additive: `fetched` is an optional key, so existing role files parse as before. It extends the `.ard/` role file contract, which is a public interface. PR body text comes only from validated fields and is redacted and secret-scanned. The proposal edits describe security behaviour, so wording must match landed code. No protected path from `merge.protected_paths` is touched (`docs/proposal.md`, `internal/` only); `deploy/` and the chart comment (#296) are out of scope.

### Size
M to L (about 250 to 450 changed lines with tests, docs prose included). Files touched: `internal/closeout/rolefile.go`, `internal/closeout/worker.go`, `internal/closeout/rolefile_test.go`, `internal/worker/run.go`, `internal/worker/pr.go`, `internal/worker/unit_test.go`, `internal/worker/loop_test.go`, `docs/proposal.md`.
