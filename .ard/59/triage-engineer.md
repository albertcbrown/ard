---
role: engineer
status: done
attempt: 1
---
Adds a `roles_repo` configuration key (repo plus pinned ref, validated like sidecars) and has the config source merge that repo's role directories into the config snapshot, with any local directory of the same name winning. Size L, risk medium because role prompts are trust-sensitive and the config load path is shared by every loop.

## Open issues
- A maintainer should decide whether shared roles need an extra approval gate. A moving branch ref in the shared repo changes roles without a PR here, so the plan recommends pinning a full commit SHA.
- The dependency on #11 looks satisfied, but the evidence is only its absence from the open list.
- The two-repository done condition can only be shown with tests until a second repository is onboarded.
- Turning the key on in this repository's own `.ard/config.yaml` is a protected-path change for a human.
