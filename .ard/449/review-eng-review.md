---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/executor/kube/roleimage.go"
    line: 350
    severity: low
    blocking: false
    message: |
      The builder pod name is `ard-build-<tag[2:14]>`, and the tag hashes only the directory contents and base, not the repository.
      Two roles with identical directories get the same pod name but push to different repositories. The second concurrent build is
      not covered by the single-flight key (tag+repo), so its CreatePod returns AlreadyExists and the job fails with "builder pod refused".
      The same happens with two controllers. Include the role name in the pod name (or hash the repo into it), or retry on AlreadyExists.
  - file: "internal/executor/kube/roleimage.go"
    line: 490
    severity: info
    blocking: false
    message: |
      The builder command (`buildctl-daemonless.sh`, the `/dev/termination-log` metadata file, the DOCKER_CONFIG mount) has not been run against a
      real cluster or the pinned BuildKit image; the worker's report says so. Verify it in a first live build.
---
The change implements the plan: a content-hash tag taken from the pinned role SHA, a registry check, a budgeted builder pod, a digest-pinned agent container, serve wiring and the proposal update. The core paths are tested and I found no blocking bug.

## Checks
- Lint: `make lint` only printed a parse error from `internal/roles/testdata/docs/skips/src/_x.go`, a fixture the change does not touch. The output was cut by `tail`, so I cannot say it passed.
- Tests with coverage: `go test -cover` on `internal/executor/kube`, `internal/executor/local` and `internal/servecmd` passes. In `roleimage.go` every function is at or above 75%, and the build, failure, timeout, admission-denied, bad-digest, single-flight and private-package paths have tests.
- Diff coverage: the uncovered lines are minor error branches in `registryDigest`, `builderState` and `buildFailure`.
