# Plan for #224

## Implementation

### Steps
Design choice (for a maintainer to confirm): a read-only mount of the controller's bare clone (`Job.RepoDir`), not a `git bundle`. The clone is already a full, non-shallow bare repo (`internal/servecmd/clone.go`, `gitx.FetchRef`), so nothing is copied, and git objects are append-only so a concurrent fetch is safe. A bundle can replace the mount later behind the same `Paths.History` field.

1. `internal/executor/executor.go`: add `Job.History bool` (new; job may read git history). Add `History string` to `Paths` (new; where the job sees the bare clone) and `HistoryDir string` to `Handle` (new; host path, "" = none). Old stored handles decode with the zero value.
2. `internal/executor/validate.go`: in `Job.Validate`, reject `History` unless `Stage == "scanner"` (same style as the `TrustedInput` case). Test in `validate_test.go`.
3. `internal/executor/local/local.go` `Prepare`: when `j.History`, set `h.HistoryDir = j.RepoDir` and `h.View.History = j.RepoDir` (host mode). In the Dockerfile branch set `h.View.History = "/history"`, keeping Work/Role/ConfigDir as today.
4. `internal/executor/local/docker.go` `runArgv`: when `h.HistoryDir != ""` append `-v <HistoryDir>:/history:ro`. `internal/executor/local/env.go` `ardEnv`: add `ARD_HISTORY=<View.History>` when set. Never mount it writable. Add `-e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=/history` (fixed values, not secrets) so git accepts the mount; confirm in the Docker integration test whether it is needed.
5. `internal/config/types.go` and `internal/config/load.go`/`raw.go`: decide how a scanner asks for history. Smallest option with no new config key: a role-directory marker file `history` (empty file, listed in `Role.Extra` today) detected by a new `Role.ReadsHistory() bool` (true for a deterministic scanner whose Extra contains `history`). Do not add an override key in `.ard/config.yaml`, so a repo override cannot grant history access to another scanner.
6. `internal/scanner/engine_exec.go` (around line 119): set `ej.History = role.ReadsHistory()`.
7. New `internal/roles/scanners/trufflehog-history/`:
   - `Dockerfile`: same pins as `scanners/trufflehog/Dockerfile` (same base digest, trufflehog 3.100.0, jq 1.8.2, checksums, TMPDIR=/var/tmp). Reuse its comments, including the AGPL note.
   - `history`: empty marker file from step 5.
   - `scan`: copy of `scanners/trufflehog/scan` with these changes. Replace the `trufflehog filesystem . ...` call by `trufflehog git "file://$ARD_HISTORY" --json --no-update --no-color --no-verification --no-ignore-tag --fail-on-scan-errors --concurrency=2` with no `--since-commit`, `--max-depth`, `--branch` or `--filter-*` flags. Exit 2 if `ARD_HISTORY` is unset or not a directory. Keep the fail-closed summary check and the hash stage with the identical salt `ard-trufflehog-v1` so fingerprints equal the HEAD scanner's. Drop the "non-empty tree scanned nothing" check; use "repository has commits but zero chunks" instead (`git --git-dir "$ARD_HISTORY" rev-list -n1 --all`). Keep `exclude-paths` handling only if trufflehog's git mode supports path exclusion (`-x`); otherwise filter in jq on the projected file path.
   - `findings.jq`: copy with `project` reading `.SourceMetadata.Data.Git` (`file`, `line`) instead of `.Filesystem`; the projected JSON keeps only d, f, l (do NOT keep commit, email, timestamp or repository). Body text changed: history findings never autoclose, so say "closing this issue does not remove the value from history; rotate it", and drop the "closes itself once gone from HEAD" wording and the "history is not scanned" line. Same `code` helper and path sanity checks.
   - `exclude-paths`: same human-owned list as trufflehog (`.ard/<n>/logs/`, `vendor/`, `node_modules/`).
   - To avoid drift, factor nothing across roles (each role dir is mounted alone); duplicate, and add a test asserting the hash lines and salt match.
8. `internal/roles/roles.go`: add `scanners/trufflehog-history` to the `go:embed` line and the doc comment. `internal/roles/config.yaml`: add `scanners/trufflehog-history: { every: 7d, max_open: 5, group_size: 1, autoclose: false, route: needs-human, timeout: 60m }` below the trufflehog line, with a comment. Check the config loader accepts `7d`; otherwise use `168h`.
9. `internal/initcmd/body.go` (line ~39): one sentence for the new scanner and how to opt out. Update `initcmd_test.go` expectations.
10. `docs/layout.md` and `docs/TODO.md`: mention the scanner and the `history` marker; the executor contract in `docs/proposal.md` if it lists mounts.
11. Kubernetes executor is not in the repo yet: leave a comment on `Paths.History` that a k8s executor must supply a read-only copy (for example an init container running `git clone --mirror` or a bundle volume).

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`.
- `internal/executor/validate_test.go`: `History` on stage triage/worker/review is `ErrInvalidJob`; on scanner it validates.
- `internal/executor/local/local_test.go`: host-mode `Prepare` with `History` gives `View.History == RepoDir`; without it both are empty.
- `internal/executor/local/docker_test`-style argv test (see `runArgv` tests in `misc_test.go`/`local_test.go`): argv contains `-v <repo>:/history:ro` only when `HistoryDir` is set, never without `:ro`; `ARD_HISTORY=/history` in env.
- `internal/executor/local/docker_integration_test.go` (skipped without Docker): a container with History runs `git --git-dir /history log --all` and sees more than one commit; a write to `/history` fails.
- `internal/config/*_test.go`: `ReadsHistory` true only for a deterministic scanner with the marker file; false for LLM roles and for trufflehog.
- `internal/scanner` engine test: a role with the marker produces `executor.Job.History == true`; others false.
- `internal/roles/roles_test.go`: add the new files to the embedded file list (line 32), `scan` executable (line 46), and a `TestEmbeddedTrufflehogHistoryRoleLoadsAsScanner` checking Every 168h, Autoclose false, MaxOpen 5, Route needs-human, a Dockerfile and the timeout.
- New `internal/roles/trufflehog_history_test.go`, reusing the fake-binary helpers in `trufflehog_test.go`: (a) same secret in HEAD-scanner and history-scanner output yields the same fingerprint; (b) a git-source result is projected with file/line only; (c) no marker string (Raw, RawV2, Redacted, SecretParts, ExtraData, commit, email) appears in stdout, stderr or any temp file; (d) fails closed: trufflehog exit non-zero, missing "finished scanning" summary, count mismatch, a result without Git metadata, bad allow entry; (e) argv contains no `--since-commit`, `--max-depth`, `--filter-*`, `--results`, and contains `--no-verification`; (f) missing `ARD_HISTORY` is a failure; missing tool exits 2; (g) Dockerfile pins.
- Edge cases: empty history; a secret in many commits is one finding with unique locations; file paths with `..` or control characters are rejected.

### Risk
Medium. Executor change touches the job sandbox: the mount must stay `:ro` and scanner-only, and history includes every fetched ref, such as unmerged PR branches. The scanner handles raw secrets in pipes, so the no-leak tests are essential. No protected path in `merge.protected_paths` is touched (the new role lives under `internal/roles/`, not `.ard/scanners/`; the `Dockerfile` rule matches only the root-level file). A repository that wants the scanner running still needs a maintainer-made `.ard/scanners/trufflehog-history/` copy and `.ard/config.yaml` override. No migration; old handles keep working. Concurrency: the scan reads the clone while fetches run, which git tolerates.

### Size
L. Files: `internal/executor/executor.go`, `internal/executor/validate.go`, `internal/executor/local/{local,docker,env}.go`, `internal/config/types.go`, `internal/scanner/engine_exec.go`, `internal/roles/{roles.go,config.yaml}`, `internal/roles/scanners/trufflehog-history/{Dockerfile,history,scan,findings.jq,exclude-paths}`, `internal/initcmd/body.go`, docs, and the matching tests. Roughly 900 to 1,300 changed lines, mostly the copied scan script, jq and tests; it can be split into the executor part and the scanner part if it passes the warn limit.
