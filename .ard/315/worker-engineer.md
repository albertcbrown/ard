---
role: engineer
status: done
attempt: 1
---
Adds scoped read-only installation token minting and revocation to the GitHub package, and a new leaf package holding the controller-to-pod contract for the Kubernetes executor. It follows design revision 3 (no controller job API, key table or TLS), so `internal/jobapi` is not created.

## Changes
- `internal/github/auth.go`: records the installation id when it is configured or discovered, plus an `installationID` accessor.
- `internal/github/scoped.go`: `ScopedToken` (value hidden from String, %#v and slog), `MintReadToken` (contents:read, repos limited to the installation owner, no redirects), `RevokeToken` (204 and 401 are success), `ScopedMinter`, `ErrCannotScope`, `ErrNotInInstallation`.
- `internal/jobpod/`: `contract.go` (containers, UIDs, mounts, Secret items, result route, labels, `PodName`, `RepoLabel`, `LabelValue`), `manifest.go` (`Manifest`, `Encode` and `Decode` with a 768 KiB cap on the encoded size and path and SHA validation), `secret.go` (`NewKey`, `SecretData`, `KeyMatches`). Imports only executor and gitref.
- `docs/layout.md`: `internal/jobpod/` entry. `docs/proposal.md`: k8s scoped-token clause in the sidecar-auth sentence.

## Testing
`go test ./internal/github ./internal/jobpod ./internal/docscheck` pass; gofmt and `go vet` clean on the changed packages. New tests: mint (body scope, JWT, 201, 422, other owner with zero requests, redirect not followed, 500), revoke, PAT refusal, token not logged; manifest round trip, size cap boundary, refusals; contract naming and label validity; key helpers. A full `go test ./...` run exceeded the tool's time limit, so I did not get its result.

## Open issues
- `make docs-check` could not run here because mkdocs is not installed.
- The full `go test ./...` result is unknown (timed out in this environment).
- Label keys use an `ard.albertcbrown/` prefix, which the plan did not specify; change it if the chart (#319) uses another.
