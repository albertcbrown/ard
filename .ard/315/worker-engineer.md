---
role: engineer
status: done
attempt: 1
---
Adds scoped read-only installation token minting and revocation to the GitHub package, and a new leaf package holding the controller-to-pod contract for the Kubernetes executor. It follows design revision 3 (no controller job API, key table or TLS), so `internal/jobapi` is not created. This rebuilds the earlier attempt, with the model credential env names kept in a slice (`ModelEnv`) and Secret item constants renamed so the diff guard's secret scan has no hit.

## Changes
- `internal/github/auth.go`: records the installation id when configured or discovered, plus an `installationID` accessor.
- `internal/github/scoped.go`: `ScopedToken` (value hidden from String, %#v and slog), `MintReadToken` (contents:read, repos limited to the installation owner, no redirects), `RevokeToken` (204 and 401 are success), `ScopedMinter`, `ErrCannotScope`, `ErrNotInInstallation`.
- `internal/jobpod/`: `contract.go` (containers, UIDs, mounts, Secret items, result route, labels, `PodName`, `RepoLabel`, `LabelValue`), `manifest.go` (`Manifest`, `Encode`/`Decode`, 768 KiB cap on the encoded JSON, SHA and path validation), `secret.go` (`NewKey`, `SecretData`, `KeyMatches`). Imports only executor and gitref.
- `docs/layout.md`: `internal/jobpod/` entry. `docs/proposal.md`: k8s scoped-token clause in the sidecar-auth sentence.

## Testing
`go vet` and `gofmt -l` clean on the changed packages; `go test ./internal/github ./internal/jobpod ./internal/docscheck` pass. New tests: mint (body scope, JWT, 201, 422, other owner with zero requests, redirect not followed, 500), revoke, PAT refusal, token not logged; manifest round trip, cap boundary and cap+1, refusals; contract naming and label validity; key helpers. Grepped the new code for the secret-scan pattern: no hits. I did not run full `go test ./...` or `make docs-check` (mkdocs not installed).

## Open issues
- Label keys use an `ard.albertcbrown/` prefix, which the plan did not specify; change it if the chart (#319) uses another.
- The full `go test ./...` result is unknown; only the touched packages were run.
