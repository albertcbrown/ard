# TODO

Open issues by phase. Generated from the GitHub milestones; closed issues are removed.

## Phase 0: Skeleton

- #26 Gate 1: one-week dry-run soak

## Phase 1: First loop

None.

## Phase 2: All roles

- #40 Roles: triage/product-manager, architect, designer, documentation-planner
- #45 Role: reviewers/product-manager-review
- #47 Scanner: lts
- #48 Scanner: lint
- #50 Scanner: missing-tests
- #52 Scanner: architecture (LLM)
- #53 Gate 3: half of scanner issues merge untouched
- #203 Scanner: trufflehog (leaked secrets, human-routed)
- #204 LLM scanner engine: run prompt.txt scanners as agent jobs
- #213 Scanner: missing-tests, part 2: scan, Dockerfile and starter wiring
- #216 Scanner: trufflehog role (#203 part 2)
- #217 Scanner: trufflehog starter wiring and image pin (#203 part 3)
- #221 Scanner failure streaks are visible on the status issue
- #231 Scanner decode errors must not echo scanner output into logs
- #254 Log secret scan: generic-assignment flags token counts and Token fields
- #255 Conflict rounds: the worker resolves merge conflicts with main
- #267 PR watch: check ard's open PRs for status changes every 5 minutes (configurable)
- #268 Committer: TestBranchesStress flakes on a git worktree admin-dir race

## Phase 3: Scale

- #54 Kubernetes executor
- #55 Default ard-agent image and role image builds
- #56 Agent adapters: kimi and dsh-tui
- #57 Budgets: daily dollar caps per role and repo
- #58 Status issue daily digest
- #59 Shared roles repo
- #60 Onboard a second repository
- #63 Limits-aware scheduling: stop starting agent jobs as the Claude subscription nears its 5-hour or weekly limit
- #64 Token caps per role, alongside #57's dollar caps
- #183 Version every PR merge to main as vX.Y.Z
- #205 Reviewer aid step: run pinned deterministic tools before a reviewer agent
- #224 Scanner: trufflehog history scan
- #237 Budgets: daily dollar caps per role and repo, part B: status-issue notice
- #239 Limits-aware scheduling: stop starting agent jobs as the Claude subscription nears its 5-hour or weekly limit, part B: thresholds and the one-job rule
- #240 Limits-aware scheduling: stop starting agent jobs as the Claude subscription nears its 5-hour or weekly limit, part C: notices
- #241 Token caps per role, alongside #57's dollar caps, part A: token ledger
- #242 Token caps per role, alongside #57's dollar caps, part B: token check in the claim gate
- #243 Default ard-agent image and role image builds, part 55a: local ard-agent image
- #244 Default ard-agent image and role image builds, part 55b: default image under the local executor
- #245 Default ard-agent image and role image builds, part 55c: role image builds for k8s
- #246 Version every PR merge to main as vX.Y.Z, part B: version workflow (human PR)
- #247 Version every PR merge to main as vX.Y.Z, part C: versioncheck tests, ldflags and docs
- #248 Version every PR merge to main as vX.Y.Z, part D: owner settings checklist (human task)

## Phase 4: Docs and site

- #178 User docs and a GitHub Pages site for ard

## Phase 5: Distribution

- #177 Install ard with Homebrew: releases and a cask
