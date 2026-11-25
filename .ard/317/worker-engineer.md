---
role: engineer
status: done
attempt: 1
---
Adds the Kubernetes API layer for the executor: minimal JSON types, a `Client` interface, an in-cluster REST implementation (standard library only) and an in-memory fake. The pod builder and executor are a later part, as the plan narrows this one.

## Changes
- `internal/executor/kube/api/types.go`: the pod, secret, event and status wire types, with omitempty and k8s camelCase tags.
- `api/client.go`: `Client` interface, `ErrNotFound`, `ErrConflict`, `StatusError`.
- `api/rest.go`: `InCluster(RESTOptions)`. TLS 1.2+ with only the given CA, token re-read on every request, redirects refused, error bodies capped at 512 bytes, DNS-label validation of names and namespaces (an empty name is rejected rather than hitting the collection), unbuffered log follow without the client timeout, delete 404 treated as success.
- `api/fake/fake.go`: mutex-guarded fake with SetPhase, SetContainer, SetPodIP, Evict, SetLogs (timestamped lines, `LogLimit` to end streams early so a caller resumes via sinceTime), AddEvent, `Err` injection and `Calls()`.
- `docs/layout.md`: one line for the new package.

## Testing
`go test ./internal/executor/kube/...` passes, and gofmt and go vet are clean. Added `rest_test.go` (token re-read, no redirect, error table, create body and round trip, base64 Secret data, log query, delete propagation, bad names make no request, selectors, InCluster errors) and `fake/fake_test.go` (round trip, conflict, evict, log resume, injected errors, call order, selectors, events). One test failed at first: an empty name reached the list endpoint. Fixed by validating names for single-object calls.

## Open issues
- `make lint` fails on a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file I did not touch. I did not run the full `make test`.
- The fake's `SetPhase` and the other helpers take a namespace argument, which the plan's signatures did not list.
