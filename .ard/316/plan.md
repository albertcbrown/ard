# Plan for #316

## Implementation

Adopts the design-revision-3 plan in the comments (pre / exec / post), not the older fetch/exec/upload text in the issue body. Corrections against main: the shared workspace package is `internal/executor/workspace` (not `internal/workspace`); `jobpod.PreSecretDir`, `PostSecretDir`, `ItemManifest`, `ItemGitAccess`, `ItemResultKey`, `ResultPort`, `ResultPath`, `RootWork`, `RootOut`, `StdoutName`, `DoneName`, `ExitPermanent`, `ExitTransient`, `MountWork/Job/Out/Ctl/Bin`, `JobRole/JobSidecar/JobHistory/JobHome`, `AgentBin`, `Decode`, `KeyMatches` all exist already. Scrub and inputs come from the decoded manifest (`Manifest.Scrub`, `Manifest.Inputs`); the manifest has no TLS or fingerprint, and post serves plain HTTP.

### Steps

1. `internal/gitx/local.go` (new method, after `ExportTree`): `func (g *Git) CheckoutTree(ctx context.Context, gitDir, workTree, sha string) error` running `git --git-dir <gitDir> --work-tree <workTree> -c core.hooksPath=/dev/null checkout --detach --force <sha>` through the package's existing git runner. It keeps symlinks and exec bits and writes no `.git`. Reason: `ExportTree` refuses symlinks and caps at 64 MiB. Add the test (step 8) in the same step so the build stays green.
2. `internal/cli/app.go`: add `JobPre`, `JobExec`, `JobPost` fields to `Handlers`, and the types `JobPreOpts{Deadline time.Time; GitBase string}`, `JobExecOpts{Deadline time.Time; Grace time.Duration; Argv []string}`, `JobPostOpts{Deadline time.Time}`. New `internal/cli/cmd_job.go` with three `Hidden: true` cobra commands `job-pre` (`--deadline`, `--git-base` default `https://github.com`), `job-exec` (`--deadline`, `--grace` default 10s, argv after `--` via `ArgsLenAtDash`; no argv returns `&ExitError{Code: 2}`), `job-post` (`--deadline`). `--deadline` is RFC 3339 UTC, parsed in cli; a bad value exits 2. Register in the root command like the other `cmd_*.go` files (follow `cmd_setup.go`). The nil-handler stub behaviour already applies.
3. `internal/jobpre/pre.go` (new): `func Run(ctx context.Context, e *cli.Env, o cli.JobPreOpts) error`. Paths (secret dir, `/ctl`, `/work`, `/job`, `/ard-bin`, termination log) sit in an unexported package `paths` var so tests can redirect them; defaults come from `jobpod`.
   - Read `manifest.json` and `token` from `jobpod.PreSecretDir`; `jobpod.Decode`. The token goes to git only as a `gitx.TokenFunc` (env via `pushEnv`), never argv. Set `HOME=/ctl/home`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null` and umask 002.
   - `InitBare(/ctl/repo.git)`; `FetchShallow(url, "+<BaseSHA>:refs/ard/base")`; when `Role.SHA != BaseSHA`, a second fetch of `Role.SHA`; `RevParse` must equal each SHA. With `History`, a full fetch of heads into `jobpod.JobHistory`. URL is `o.GitBase + "/" + Repo + ".git"`.
   - `CheckoutTree(/ctl/repo.git, /work, BaseSHA)`. Role: `ExportPaths(..., Role.SHA, /ctl/role-x, Role.Path)`, rename `role-x/<Path>` to `/job/role`; a `Role.Repo` other than `Repo` is refused. Sidecar: `workspace.FetchPinned(ctx, g, /ctl/sidecar.git, url, sha, tok)` then `ExportTree` to `/job/sidecar`.
   - `workspace.Populate({Tree: "/work", Home: "", Scrub, Inputs})`. Dirs `g+ws`; `workspace.ChmodReadOnly` on role, sidecar and, when `ReadOnly`, the tree.
   - Copy `os.Executable()` to `jobpod.AgentBin` 0755 via temp file and rename.
   - `gitx.ErrRefNotFound`, `gitx.ErrRepoAccess`, a bad manifest or an input over a symlink: write the reason to `/dev/termination-log` and return `&cli.ExitError{Code: jobpod.ExitPermanent}`; other errors exit `jobpod.ExitTransient`. Never log the token.
4. `internal/jobexec/exec.go` (new): `Run(ctx, e, o cli.JobExecOpts) error`; reads no credential or Secret path.
   - `workspace.MakeHome(jobpod.JobHome)` first. Child via `exec.Command` with `SysProcAttr{Setpgid: true}`, stdin `/dev/null`, inherited env, stdout to `io.MultiWriter(os.Stdout, /out/stdout)`, stderr to `os.Stderr`.
   - At `Deadline`, or on SIGTERM/SIGINT: SIGTERM to `-pgid`, SIGKILL after `Grace`. While the child runs, reap orphans with `wait4(-1, WNOHANG)` on SIGCHLD, skipping the child's pid until `cmd.Wait` returns (put the syscall code in a `_unix.go` file with a build tag if the package must still compile elsewhere).
   - After exit: kill `-pgid`; add `g+r` (dirs `g+x`) on files in `/work`; fsync stdout; write `/out/done` with `O_TRUNC`. Exit code is the child's, 124 on deadline, `128+sig` on a signal, via `cli.ExitError`.
5. `internal/jobpost/post.go` (new): `Run(ctx, e, o cli.JobPostOpts) error`.
   - Read `jobpod.PostSecretDir/result-key`. `net.Listen(":8080")` at start; failure returns an error (exit 1). `http.Server` with `ReadHeaderTimeout` 10 s, `IdleTimeout` 60 s, `MaxHeaderBytes` 8 KiB, discarded `ErrorLog`, a private `http.NewServeMux` with only `GET /v1/result`.
   - Handler order: auth (`jobpod.KeyMatches`, fixed 401 body); a one-slot semaphore (429); 409 until `/out/done` exists, with no read of `/work`; then stream `tarball.Pack(w, {RootWork, /work}, {RootOut, /out, Files: [StdoutName]})`. Repeat GETs are served. No header is ever logged.
   - SIGTERM or `Deadline`: `Shutdown` with 10 s, exit 0.
6. `cmd/ard/main.go` `handlers()`: add `JobPre: jobpre.Run`, `JobExec: jobexec.Run`, `JobPost: jobpost.Run` and the imports.
7. `docs/layout.md`: one line each for `internal/jobpre/`, `internal/jobexec/`, `internal/jobpost/` (pod-side commands of the k8s executor; no GitHub API). Check `internal/docscheck` still passes, since it may read this file.
8. Tests (with the code they cover): `internal/gitx` test for `CheckoutTree`; `internal/jobpre/pre_test.go`; `internal/jobexec/exec_test.go`; `internal/jobpost/post_test.go`; `internal/cli` parse tests.

### Test plan

Command: `go test ./internal/gitx/... ./internal/cli/... ./internal/jobpre/... ./internal/jobexec/... ./internal/jobpost/... ./cmd/...`, then `go vet ./...` and `go build ./...`. No network beyond loopback `httptest` and `gitx/gittest` file remotes.
- `TestCheckoutTree` (gitx): symlink and exec bit kept, no `.git` in the work tree, hooks not run.
- `TestPre` (table): tree at `BaseSHA` with symlink and exec bit, no `.git`; role exported from a different `Role.SHA`; sidecar pinned; scrubbed file gone; inputs written; tree group-writable and no home made; missing SHA gives exit 3 plus a termination message; an input over a committed symlink gives exit 3; `ReadOnly` tree is `a-w`; `Role.Repo` mismatch refused; binary copied 0755; transient fetch failure exits 1.
- `TestPreTokenNotInArgv`: a fake git on PATH records argv and env; the token appears only in env.
- `TestExec` (table; the test binary re-exec'd as the fake agent): exit 0 and 3 pass through; stdout teed to file and `os.Stdout`, stderr not; deadline gives 124 and the marker; a child ignoring SIGTERM gets SIGKILL after `Grace`; forwarded SIGTERM gives `128+15`; grandchild killed; a planted `done` is truncated; a 0600 file left by the child becomes 0640; home and `.claude` are 0700; no argv exits 2.
- `TestPost` (table, `httptest`): no or wrong key gives 401 and `/work` (mode 0000) is not read; before the marker 409; after it a tar holding `work/...` and only `out/stdout`; a second concurrent request gets 429; repeat GET returns the same bytes; `/debug/pprof/` gives 404; the log lacks the key and "Authorization"; port already bound returns an error; SIGTERM or deadline exits 0.
- `cli_test.go`: `job-exec -- cmd args` and `--deadline` parse; a bad deadline exits 2; commands are hidden from help.
- Edge: unreadable manifest, oversize manifest, empty key file.

### Risk

Medium. Signals, process groups and PID-1 reaping are easy to get subtly wrong and cannot be fully unit-tested; the kind-based test in #321 is the real check. `job-post` serves a credentialed HTTP endpoint (security-sensitive, constant-time key check, no logging of headers). `job-pre` handles the git token. Public interface: new hidden CLI flags that #317 writes into the pod spec. No protected paths from `merge.protected_paths` are touched (`docs/layout.md` is not protected). No migrations.

### Size

L: about 500 lines of code and 480 of tests, around 980 changed lines, under the default 3,000 limit. Files: `internal/gitx/local.go` (+ test), `internal/cli/app.go`, `internal/cli/cmd_job.go` (new), `internal/cli/cli_test.go`, `internal/jobpre/pre.go` (+ test, new), `internal/jobexec/exec.go` (+ test, new), `internal/jobpost/post.go` (+ test, new), `cmd/ard/main.go`, `docs/layout.md`. If it runs long, move `job-post` to a second PR.
