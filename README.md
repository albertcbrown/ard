# ard — automated repository development

ard makes a GitHub repository maintain itself. Scanners file deduplicated issues, triage agents turn each issue into a plan, a worker agent implements it on a branch, reviewer agents critique the PR, and a merger lands it once every review passes.

> **Status:** design stage. Nothing runs yet. The design lives in [docs/proposal.md](docs/proposal.md), and the work is tracked as [GitHub issues](https://github.com/albertcbrown/ard/issues) grouped into phase milestones.

## How it works

One rule holds the system together: **a deterministic controller owns every GitHub and git side effect; agents only read and write files in a worktree.**

One `ard` process per repository runs six loops that share a small pool of job slots (default 3):

| Loop | Every | What it does |
| --- | --- | --- |
| Scanners | 4 h | Find problems (CVEs, outdated components, lint, docs, missing tests…), file or update issues, close what is fixed |
| Triage | 15 min | Role agents (lead, PM, architect, designer, doc planner, engineer) turn an issue into `plan.md` |
| Workers | 5 min | Implement the plan on `worker/<role>-<issue#>-<slug>`, run tests, open a draft PR |
| Reviewers | 5 min | Role agents (security, license, eng, PM) review the PR and request fixes |
| Merger | 5 min | Squash-merge PRs whose reviews all passed and whose checks are green |
| Reaper | 1 min | Mark dead or overdue jobs `ard:executor:failed`, keep their evidence 24 h |

GitHub labels prefixed `ard:` are the queue and the state machine. `.ard/<issue#>/` in the target repository holds each issue's plan, per-role summaries and full agent logs.

## Configuration by convention

A target repository opts in with an `.ard/` directory. A role exists because its directory exists:

```
.ard/
├── config.yaml          # global settings and overrides only
├── scanners/<name>/     # scan (executable) or prompt.txt; optional Dockerfile, scripts
├── triage/<name>/       # prompt.txt; optional Dockerfile, scripts
├── workers/<name>/      # prompt.txt; optional Dockerfile, scripts
├── reviewers/<name>/    # prompt.txt; optional Dockerfile, scripts
└── <issue#>/            # per-issue output and logs, written only by ard
```

See [Configuration](docs/proposal.md#configuration) in the proposal for the full schema and defaults.

## Development

Requires Go (see `go.mod`).

```bash
make build   # builds bin/ard
make test    # go test ./...
make lint    # go vet and a gofmt check
```

Contributor and agent guidance is in [CLAUDE.md](CLAUDE.md).

## License

[GPL-3.0](LICENSE)
