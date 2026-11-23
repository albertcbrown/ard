# Plan for #223

## Implementation

### Steps
1. `internal/triage/loop.go`: add a field to `Deps` (new): `PlanExists func(ctx context.Context, issue int) (bool, error)` documented as "reports whether `.ard/<issue>/plan.md` exists on `main`; nil means the check is skipped" (keeps existing callers building). Nothing else changes in this file.
2. `internal/triage/fanin.go`: add const `rNoPlan = "no-plan"` to the reasons block. In `gateIssue`, after the `t.dry` return and before `gateReady` (verdict fReady only, so waiting, skipped and other human paths are unchanged), call `t.l.d.PlanExists(ctx, n)` when non-nil. On error return `fmt.Errorf("triage gate issue %d: check plan: %w", n, err)` (the next tick retries; nothing is posted). When false, call `t.gateHuman(ctx, n, jobID, fanInResult{Verdict: fHuman, Reason: rNoPlan, Worker: res.Worker})` and return. Doing it before `gateReady` means neither the admission/body-hash check nor `askApproval` can post "ready for approval" without a plan. Do not touch `approved`/`askApproval`.
3. `internal/triage/fanin.go`, `gateHuman`: add `case rNoPlan:` with marker `<!-- ard:triage gate=no-plan job=<jobID> -->` (use `labels.Prefix` like the others) and text "Triage finished but `.ard/<n>/plan.md` does not exist, so ard will not ask for approval or start a worker. A maintainer can re-run triage (remove `ard:triage:lead:done` and `ard:needs-human`, re-apply the intake label) or write the plan by hand." Fixed text only, no issue text. It reuses the existing AddLabels(needs-human), gate event (reason `no-plan`) and log.
4. `internal/servecmd/triage.go`: set `PlanExists` in the `triage.Deps` literal. Implement it in the same package (new small func/method on `wiring`): `g := gitx.New("")`; `g.FetchRef(ctx, w.Root, w.RemoteURL, "+refs/heads/main:<ref>", w.Token)` into a private ref (reuse the seed ref pattern in `internal/servecmd/clone.go`, but a distinct ref name, new), then `g.FileAt(ctx, w.Root, <ref>, fmt.Sprintf(".ard/%d/plan.md", n), closeout.MaxRoleFileBytes)` and return its `ok`. Fetch/FileAt errors are returned (fail closed, retried next tick). Check that `wiring` exposes `RemoteURL` and `Token` (the worker wiring uses `w.RemoteURL`, `w.Token`).
5. `docs/proposal.md` (triage section, the gate paragraph near the "ready for approval" sentence): state that, before asking for approval or starting a worker, the gate checks that `.ard/<n>/plan.md` exists on `main`; if not it posts one comment, adds `ard:needs-human` and records a `gate` event with reason `no-plan`. Note that removing a role's request label by hand does not count as that role finishing.
6. `docs/runbooks/gate-2.md` (step 5 "Watch", near the "ready for approval" bullet): add one line that a missing `plan.md` produces a needs-a-human comment instead.

### Test plan
Command: `go test ./internal/triage/... ./internal/servecmd/...` (full: `make test`).
- `internal/triage/fanin_test.go`: extend `gateHarness`/`harness` (`harness_test.go`) so `Deps.PlanExists` is a fake backed by a per-harness set of issues with a plan, defaulting to "plan exists" so existing gate tests (`TestGateStartsWorker`, `TestGateApproval`, `TestGateWaits`) pass unchanged.
- New `TestGateNoPlan`: `gateHarness(t, true)`, `h.ready()` (lead done, no requested role, worker engineer named), no plan. After `mustTick` twice: `ard:needs-human` set, no `ard:worker:engineer`, `commentsWith(h, n, "ready for approval") == 0`, exactly one comment containing "plan.md does not exist", one gate event with reason `no-plan`, no further writes on the second tick (guarded by needs-human and `postOnce`).
- Same with `require_approval` false: needs-human, no worker label (a worker is not started without a plan).
- Plan present: the existing "no approval posts one comment and waits" case still posts exactly one "ready for approval".
- `PlanExists` returns an error: tick returns an error, no label, no comment.
- `--dry-run` case: no write, `triage.would_gate` only (existing dry test must stay green).
- Engineer role waiting (`ard:triage:engineer` requested, not done) with no plan: the gate still waits, no check run (`TestGateWaits`).
- `internal/servecmd`: a unit test of the PlanExists helper against a temp git repo with and without `.ard/<n>/plan.md` on `main`, if the existing clone tests give a cheap fixture; otherwise cover through `triage_e2e_test.go`, seeding a plan in the e2e fixture where a worker is expected to start.

### Risk
Medium. The gate is the last check before a worker starts, and the check adds a git fetch per gated issue per tick. It fails closed on errors. Lead-only triage runs (no other role) never write `plan.md` and will now stop at needs-human; that matches the issue's expected behaviour but is a behaviour change. `Deps` gains a field (internal, optional). No protected paths from `.ard/config.yaml` are touched (docs and Go code only); no migrations; no concurrency changes.

### Size
M (about 120-200 changed lines including tests). Files: `internal/triage/loop.go`, `internal/triage/fanin.go`, `internal/triage/fanin_test.go`, `internal/triage/harness_test.go`, `internal/servecmd/triage.go` (plus a new helper file or addition in `internal/servecmd/`), possibly `internal/servecmd/triage_e2e_test.go`, `docs/proposal.md`, `docs/runbooks/gate-2.md`.
