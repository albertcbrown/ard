# Plan for #317

## Implementation

Scope: only the API layer (part 6A as narrowed by the latest design-revision-3 plan): `internal/executor/kube/api` and `api/fake`. The pod builder, status mapping, log follower and executor are a later part and are NOT planned here. No `client-go`; standard library only (static, CGO-free build). Nothing exists under `internal/executor/kube` yet; every file below is new.

### Steps

1. **`internal/executor/kube/api/types.go`** (new): minimal JSON structs, `omitempty` on everything optional, json tags in k8s camelCase (`initContainers`, `activeDeadlineSeconds`, `secretKeyRef`, `ownerReferences`, `containerStatuses`...).
   - `ObjectMeta{Name, Namespace, UID, Labels, Annotations, OwnerReferences, CreationTimestamp}` (tag `metadata`), `OwnerReference{APIVersion, Kind, Name, UID}`.
   - `Pod{Meta; Spec PodSpec; Status PodStatus}`, `PodSpec`, `Container`, `EnvVar`/`EnvVarSource`/`SecretKeySelector`, `VolumeMount`, `Volume` (`EmptyDirVolumeSource{SizeLimit}`, `SecretVolumeSource{SecretName, Items []KeyToPath, DefaultMode}`), `ContainerPort`, `SecurityContext`, `PodSecurityContext` (adds `FSGroup`), `Capabilities`, `SeccompProfile`, `ResourceRequirements{Requests, Limits map[string]string}`, as listed in the issue's revision-3 plan.
   - `PodStatus{Phase, Reason, Message, PodIP, InitContainerStatuses, ContainerStatuses}`, `ContainerStatus`, `ContainerState{Waiting, Running, Terminated}` with the fields from the plan.
   - `Secret{Meta; Type; Data map[string][]byte}` (encoding/json base64 matches the API), `Event{Meta; InvolvedObject; Reason, Message, Type; LastTimestamp}`, `Status{Code, Reason, Message}`.
   - Reason: one place defining the wire shapes the executor will consume.
2. **`api/client.go`** (new): `type Client interface` with CreatePod, GetPod, ListPods, DeletePod(graceSec int64), PodLogs(sinceTime) (io.ReadCloser), CreateSecret, DeleteSecret, ListEvents, exactly the signatures in the revision-3 plan. Also `var ErrNotFound`, `var ErrConflict`, and `type StatusError struct{Code int; Reason, Message string}` with `Error()`. No exec, no Secret get/list, no patch.
3. **`api/rest.go`** (new): `type RESTOptions struct{Host, TokenFile, CAFile string; Timeout time.Duration}` and `func InCluster(o RESTOptions) (Client, error)`.
   - Zero-value defaults: host from `KUBERNETES_SERVICE_HOST`/`_PORT` (use `net.JoinHostPort`), token `/var/run/secrets/kubernetes.io/serviceaccount/token`, CA `.../ca.crt`; TLS MinVersion 1.2 with a pool containing only that CA.
   - Token file re-read on every request (small `do` helper sets `Authorization: Bearer`).
   - `CheckRedirect` returns `http.ErrUseLastResponse` (same rule as `internal/github/http.go:215`); a 3xx is turned into an error.
   - Non-2xx: decode body (read limit) into `Status`; 404 → `ErrNotFound`, 409 → `ErrConflict`, else `*StatusError` with Message capped at 512 bytes. Never log headers.
   - Names/namespaces validated as DNS labels (regexp `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, ≤63) before building paths; query via `url.Values`.
   - DeletePod: DELETE with JSON body `{"propagationPolicy":"Background","gracePeriodSeconds":N}`; 404 → nil. DeleteSecret: 404 → nil.
   - PodLogs: GET `.../pods/{name}/log?follow=true&timestamps=true&container=…&sinceTime=<RFC3339Nano, omitted when zero>`; returns the body unbuffered (the log request must not use the overall client Timeout; use a separate no-timeout client or per-request ctx for this call).
   - ListPods uses `labelSelector`, ListEvents uses `fieldSelector`.
4. **`api/fake/fake.go`** (new, package `fake`): in-memory `Client`, mutex-guarded.
   - `CreatePod` assigns UID and phase `Pending`, ErrConflict on duplicate; Get/List (label selector `k=v,k2=v2` equality only); DeletePod removes (404 = nil); Secrets likewise.
   - Helpers: `SetPhase`, `SetContainer(pod, name string, st api.ContainerState)`, `SetPodIP`, `Evict(name)` (phase Failed, reason Evicted), `SetLogs(pod, container string, lines []string)` (scripted stream that can end early so a later PodLogs call resumes after `sinceTime`), `AddEvent`, `Err map[string]error` keyed by method name, `Calls() []string`.
   - Add compile-time check `var _ api.Client = (*Fake)(nil)`.
5. **Tests** (see plan below): `api/rest_test.go`, `api/fake/fake_test.go`.
6. **`docs/layout.md`**: add a line after line 19 for `internal/executor/kube/api/` (minimal k8s REST types, client, in-cluster transport; fake/ test double), matching the existing column alignment.

### Test plan

Command: `go test ./internal/executor/kube/...` then `make test` and `make lint` (go vet, gofmt). No network; use `httptest.NewTLSServer` with `RESTOptions{Host, TokenFile, CAFile}` (write the server cert to a temp CA file) in `api/rest_test.go`.
- `TestTokenReread`: write token "a", call GetPod, rewrite token "b", call again; server saw `Bearer a` then `Bearer b`.
- `TestNoRedirect`: server answers 302; call returns an error and the redirect target is never hit.
- `TestErrors` (table): 404 → `errors.Is(ErrNotFound)`; 409 on CreateSecret → `ErrConflict`; 403 with Status JSON → `*StatusError` Code 403 and Reason; 10 KiB body → Message ≤ 512 bytes; non-JSON body still yields a StatusError.
- `TestCreatePodBody`: marshal a pod with init container, `secretKeyRef` env and Secret items; server decodes into `map[string]any` and asserts keys `initContainers`, `activeDeadlineSeconds`, `secretKeyRef`, `items`, `metadata`; response round-trips including status.
- `TestPodLogsQuery`: query has `follow=true`, `timestamps=true`, `container=agent`, `sinceTime=`; zero time omits sinceTime; body streamed.
- `TestDeletePodPropagation`: body contains `propagationPolicy: Background` and `gracePeriodSeconds`; 404 returns nil. Same 404-nil for DeleteSecret.
- `TestBadName`: `../x`, empty, uppercase, >63 chars for name and namespace rejected with no request made (server counter 0).
- `TestListSelectors`: label/field selectors are URL-encoded in query.
- `TestInCluster defaults`: unset env and no Host gives an error; missing CA file gives an error.
- `fake_test.go`: create/get/delete round trip; duplicate create → ErrConflict; `Evict` sets Failed/Evicted; log stream ends early then a second PodLogs call resumes; injected `Err["GetPod"]` is returned; `Calls()` records order; label-selector list.

### Risk

Low–medium. New internal package, no public interface, migration, concurrency beyond a mutex in the fake, or protected path touched (`docs/layout.md` and `internal/` are not in merge.protected_paths). Security-relevant parts: bearer token handling (re-read, never logged), no redirects, path-injection validation, CA pinning. Wrong JSON field names would show only against a real API server; `TestCreatePodBody` pins them and the later kind test is the backstop.

### Size

L, lower end: about 420 lines of code and 330 of tests (about 750), under the worker's block limit. Files: `internal/executor/kube/api/types.go`, `client.go`, `rest.go`, `rest_test.go`, `internal/executor/kube/api/fake/fake.go`, `fake_test.go`, `docs/layout.md`.
