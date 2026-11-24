# Plan for #344

## Implementation

### Steps
1. `internal/committer/init.go`: add `ard-init\.lock` to the alternation in `initPathRe` (next to `config\.yaml`) and update the `CheckInitPath` doc comment, so `.ard/ard-init.lock` is an accepted path. Extend the `CheckInitPath` test table in `internal/committer` (accept the lock; still reject `.ard/ard-init.lock/x`, `.ard/ard-init.lockx`).
2. `internal/roles/config.yaml` (the starter template, not this repo's `.ard/config.yaml`): add `- .ard/ard-init.lock` to `merge.protected_paths` with a one-line comment. Check any test that asserts the starter's protected list (grep `protected_paths` in `internal/roles`, `internal/initcmd`, `internal/config`).
3. New `internal/initcmd/lock.go`:
   - `const LockPath = ".ard/ard-init.lock"`.
   - `type Lock struct { Version string \`yaml:"version"\`; Files map[string]string \`yaml:"files"\` }` (path → lowercase hex sha256).
   - `func ParseLock(data []byte) (Lock, error)` using `gopkg.in/yaml.v3` (already in go.mod). A malformed or hand-edited lock (bad YAML, wrong types, non-hex or wrong-length hashes) must not fail the run: return an empty `Lock` for bad YAML, and drop individual bad entries, so those files count as "edited". Return a warning string list for printing.
   - `func hashOf(b []byte) string` and `func (l Lock) Marshal() ([]byte, error)` with sorted keys (yaml.v3 sorts map keys) so output is deterministic.
4. `internal/initcmd/plan.go`: replace `actOverwrite` and the `force` argument.
   - Actions: `actCreate "add"`, `actUnchanged`, `actUpdate "update"`, `actEdited "edited"` (skipped, not written), `actContext` is just `actCreate`/`actUnchanged`.
   - New signature `func plan(existing fs.FS, files []committer.InitFile, lock Lock) (Result, error)` where `type Result struct { Ops []op; Dropped []string; Diffs map[string]string }`. `op` gains `Diff string` for edited files. No error for differing files anymore; keep the "is a directory on main" error and the read errors.
   - Per file, in order: context doc (`ContextPaths()`) present → unchanged, absent → create; absent → create; content and exec bit equal to the starter → unchanged; `hashOf(have) == lock.Files[path]` (entry must exist) → update; otherwise → edited with a unified diff of main vs starter (small helper `unifiedDiff(path, have, want string) string`, no new dependency; a simple line diff is enough).
   - `Dropped`: sorted paths in `lock.Files` that are not in `files` and not a context doc; listed only, never deleted.
   - Keep `changed(ops)` returning only create/update ops. Add `func withLock(res Result, existing fs.FS, version string) (committer.InitFile, bool, error)` (or inline) that builds the new lock: start from the old entries, set the starter hash for each created or updated file, leave entries for unchanged/edited files as they were, and for unchanged files with no entry add the starter hash only if absent-lock is not being trusted (keep it simple: do not add; "no entry → edited" is the stated rule). Drop nothing. Emit the lock file as an `InitFile{Path: LockPath, Data: ..., Exec: false}` only when something was created or updated or the version changed; skip it when there are no writes so "nothing to propose" still works.
5. `internal/initcmd/initcmd.go`: in step 6 export `LockPath` too (append to the `ExportPaths` list), read and `ParseLock` it from `existing`, call the new `plan`, append the lock `InitFile` to what `changed` returns before `overlay` and `Propose`, and pass the ard version in. Thread the version through `deps` or `cli.Env` (check where `cli.App.Version` is reachable; `internal/cli/app.go:79`). After the PR step, print edited files with their diffs and dropped files ("no longer in the starter, left in place"). Update `printOps` for `update`/`edited`. Make the "nothing to propose" message cover the case where only edited files remain.
6. `internal/cli/cmd_init.go` and `internal/cli/app.go`: remove the `--force` flag and `InitOpts.Force` (the planner replaces it). Update callers: `initcmd.go` and `initcmd_test.go` (`h.env(force, dry)`, `h.run`).
7. `internal/initcmd/body.go`: describe updated files, edited files left alone and dropped files in the PR body.
8. Docs: `docs/site/getting-started.md` (the `ard init` paragraph, line ~36), `docs/site/configuration.md` (line ~48: lock is protected), `docs/runbooks/onboard-repo.md` and `docs/site/operations.md` if they mention `--force`; add `.ard/ard-init.lock` to the layout description in `docs/layout.md` if `.ard/` contents are listed. Run `make` docs check (`go test ./internal/docscheck/...`).

### Test plan
Command: `go test ./internal/initcmd/... ./internal/committer/... ./internal/cli/... ./internal/roles/... ./internal/docscheck/...`, then `go test ./...`.
- Table test `TestPlanUpgrade` in `internal/initcmd/initcmd_test.go` (replace `TestPlan*` force tests around lines 577-630), each row (existing FS, lock) → action:
  - absent → add; identical to starter, with and without lock → unchanged; differs and hash equals lock → update; differs and hash differs from lock → edited with non-empty diff and not in `changed`; differs with no lock → edited; differs with lock lacking the entry → edited; exec bit differs only, content equal → follows the hash rule (update if lock matches, else edited).
  - context doc present and edited → unchanged; context doc absent → add; context doc in lock → still never updated.
  - in lock, absent from starter → in `Dropped`, never an op; also when the file still exists on main.
- `TestParseLockHandEdited`: garbage YAML, `files:` as a list, short or uppercase hashes, extra keys, empty file → no error, bad entries ignored, affected files plan as edited.
- `TestLockMarshal`: deterministic ordering; version recorded; round trip.
- Lock written: after create/update ops the produced lock contains starter hashes for exactly those paths and keeps old entries for edited files; no writes → no lock file.
- End-to-end in the existing harness: first run creates files and lock; merge; second run → "nothing to propose"; edit one file on main and bump starter hash via lock → check edited stays untouched and diff printed; dropped entry listed.
- `CheckInitPath` cases (step 1) and a starter-config test that `.ard/ard-init.lock` is in `merge.protected_paths`.
- Remove `TestInitConflictAndForce` and force subtests.

### Risk
Medium. Behaviour change to a user-facing command: `--force` is removed (breaking for scripts). `CheckInitPath` is a security gate on what init may commit, so the regex change needs tight tests. The lock is untrusted input from main (an attacker or hand edit can only cause files to be treated as edited or, by forging a matching hash, updated to the shipped starter), so parsing must be tolerant and never panic. The embedded template's protected_paths changes; this repo's own `.ard/config.yaml` is protected and is not touched, so a maintainer must add the lock there by hand if wanted. No migrations or concurrency.

### Size
M (about 300 lines including tests, possibly toward L with docs and the diff helper). Files: `internal/committer/init.go` (+test), `internal/roles/config.yaml`, `internal/initcmd/lock.go` (new), `internal/initcmd/plan.go`, `internal/initcmd/initcmd.go`, `internal/initcmd/body.go`, `internal/initcmd/initcmd_test.go`, `internal/cli/cmd_init.go`, `internal/cli/app.go`, docs under `docs/`.
