# TODO

Open issues by phase. Generated from the GitHub milestones; closed issues are removed.

## Phase 0: Skeleton

- #26 Gate 1: one-week dry-run soak

## Phase 1: First loop

- #39 Gate 2: first loop proven
- #165 serve: refuse to start on a git too old for the committer
- #172 Cost and tokens table on each issue

## Phase 2: All roles

- #40 Roles: triage/product-manager, architect, designer, documentation-planner
- #41 Review fix rounds
- #42 Reviewer selection rules
- #43 Role: reviewers/security-review
- #44 Role: reviewers/license-review
- #45 Role: reviewers/product-manager-review
- #46 Merger loop
- #47 Scanner: lts
- #48 Scanner: lint
- #49 Scanner: error-codes
- #50 Scanner: missing-tests
- #51 Scanner: docs
- #52 Scanner: architecture (LLM)
- #53 Gate 3: half of scanner issues merge untouched
- #89 Optional gitleaks pass in the log secret scan
- #138 closeout: don't revert newer plan.md sections committed after a job's base
- #142 Workers: .gitattributes changes can hide diffs from review
- #160 eng-review should run the repository's own lint target
- #161 Wake the loops without restarting ard serve
- #174 Job cleanup fails on Go's read-only module cache and leaks it

## Phase 3: Scale

- #54 Kubernetes executor
- #55 Default ard-agent image and role image builds
- #56 Agent adapters: kimi and dsh-tui
- #57 Budgets: daily dollar caps per role and repo
- #58 Status issue daily digest
- #59 Shared roles repo
- #60 Onboard a second repository
- #61 Janitor: prune old .ard/<issue#>/ directories
- #63 Limits-aware scheduling: stop starting agent jobs as the Claude subscription nears its 5-hour or weekly limit
- #64 Token caps per role, alongside #57's dollar caps
