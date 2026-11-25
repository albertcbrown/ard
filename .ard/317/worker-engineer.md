---
role: engineer
status: done
attempt: 1
---
Adds the Kubernetes API layer for the executor: minimal JSON types, a `Client` interface, a stdlib in-cluster REST client (token re-read, no redirects, name validation) and an in-memory fake. Review round 1 fixes are included.

## Changes
- `internal/executor/kube/api/types.go`: types, now with `Container.RestartPolicy *string` for native sidecars.
- `internal/executor/kube/api/client.go`, `rest.go`: client interface, errors and REST transport. Error message truncation now stops at a rune boundary.
- `internal/executor/kube/api/fake/fake.go`: in-memory fake. Secret `Data` is cloned on create and read.
- Tests for the REST client and the fake.
- `docs/layout.md`: new package line.

## Testing
Ran `go test -race ./internal/executor/kube/...`; all pass. `TestCreatePodBody` now checks that `restartPolicy` is sent on an init container.

## Review findings
- `api/types.go` RestartPolicy: fixed: field added and covered by `TestCreatePodBody`.
- `api/rest.go` message truncation: fixed: trimmed back to a rune start.
- `api/fake/fake.go` Secret Data sharing: fixed: map cloned in `CreateSecret` and `Secret`.

## Open issues
None.
