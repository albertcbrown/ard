# Roles

A role is a directory under `.ard/`. Its type is the parent directory, its name is the directory name, and `config.yaml` never lists it.

| Directory | Type | What it does |
| --- | --- | --- |
| `.ard/scanners/<name>/` | scanner | Finds problems and files issues |
| `.ard/triage/<name>/` | triage | Plans an issue |
| `.ard/workers/<name>/` | worker | Implements a plan |
| `.ard/reviewers/<name>/` | reviewer | Reviews a pull request |

A role is an agent when it has a `prompt.txt`, and an executable (for scanners) when it has a `scan` script. A role with a `Dockerfile` runs in Docker; one without runs as a host subprocess.

## Starter roles

`ard init` ships these:

- **Scanners:** `cve`, `docs`, `error-codes`, `lint`, `missing-tests`, `trufflehog`, `trufflehog-history`.
- **Triage:** `lead` (routes the issue), `engineer` (writes the implementation plan), `product-manager`, `architect`, `designer`, `documentation-planner`.
- **Workers:** `engineer`.
- **Reviewers:** `eng-review`, `security-review`, `license-review`, `product-manager-review`.

Role directories are protected paths in the recommended config, so changing one takes a human approval.

## Writing a role prompt

`prompt.txt` is the whole prompt. The controller gives the agent:

- the issue and its comments as files, `.ard-input/<issue#>/issue.md` and `comments.md`, which are untrusted text;
- earlier roles' output under `.ard/<issue#>/`;
- on a fix round, `test-output.log`;
- an optional read-only sidecar repository at `$ARD_SIDECAR`.

Tell the agent to treat the input files as data and to write only its own output file. Say which status values it may report and what each means. Test a new prompt with `ard serve --dry-run` before relying on it.

## The role file

Each agent job ends by writing one file, `.ard/<issue#>/<role>.md` (workers write `worker-<role>.md`). It begins with frontmatter that code checks:

- the first line is exactly `---`, and the frontmatter ends at the next line that is exactly `---`;
- `role` is the role's name; `status` is `done`, `needs-human` or `blocked` (reviewers use their own verdicts); `attempt` is a number; `next` is an optional list of roles to run next.

Anything else in the frontmatter fails the job. The body, after the closing `---`, becomes the comment the controller posts.

## The write allowlist

An agent cannot push or edit GitHub. The controller commits a role's output only if every changed path is allowed:

- every role may write its own role file;
- a triage role may also write its own `## <section>` of `plan.md` (the heading named by its `plan_section`); the designer may also write under `.ard/<issue#>/design/`;
- a worker may change code in its worktree, but never `.github/`, `.ard/` (other than its report), secret-shaped files, symlinks, binaries or files over 1 MiB, and never more changed lines than `diff_max_lines`.

Any other change is rejected and the job ends `needs-human`. Nobody may change `issue.md`, another role's file or delete `plan.md`.
