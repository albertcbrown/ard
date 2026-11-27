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
      Two roles with identical directories get the same pod name but push to different repositories. The single-flight key is tag+repo,
      so the second concurrent build gets AlreadyExists from CreatePod and the job fails with "builder pod refused". Two controllers
      hit the same case. Include the role name in the pod name (or hash the repo into it), or retry on AlreadyExists.
  - file: "internal/executor/kube/roleimage.go"
    line: 348
    severity: info
    blocking: false
    message: |
      The builder command (`buildctl-daemonless.sh`, the termination-log metadata file, the DOCKER_CONFIG mount) has not been run against a
      real cluster or the pinned BuildKit image. Verify it in the first live build.
---
The change follows plan.md: content-hash tag from the pinned role SHA, registry check, budgeted builder pod, digest-pinned agent container and serve wiring. I found no blocking bug, so this is approved. The head only merges main into the branch, and the role image code is unchanged since the last round.

## Checks
- Lint: not re-run this round. The head differs from the reviewed commit only by a merge of main, and the last run showed no problem on the changed lines.
- Build and tests: `go build ./...` is clean, and `go test` passes for `internal/executor/kube` and `internal/servecmd`.
- Diff coverage: not re-measured. Last round the build, failure, timeout, admission-denied, bad-digest, single-flight and private-package paths were covered. The uncovered lines were minor error branches.

## Previous findings
- internal/executor/kube/roleimage.go (pod name collision between roles with identical directories): still present, now at line 350. It stays low and non-blocking.
- internal/executor/kube/roleimage.go (builder command unverified on a real cluster): still present as an info note, now at line 348.
