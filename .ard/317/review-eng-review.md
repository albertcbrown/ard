---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/executor/kube/api/types.go"
    line: 1200
    severity: high
    blocking: true
    message: |
      Container has no `RestartPolicy *string` field, which plan.md step 1 requires for the native sidecar.
      Without it part B cannot declare the upload sidecar as an init container with `restartPolicy: Always`.
      Add `RestartPolicy *string `json:"restartPolicy,omitempty"``, and have TestCreatePodBody check that it is sent.
  - file: "internal/executor/kube/api/rest.go"
    line: 725
    severity: low
    message: |
      Truncating `st.Message` at 512 bytes can cut a multi-byte UTF-8 character in half.
      Trim back to a rune boundary, for example with utf8.ValidString and a loop.
  - file: "internal/executor/kube/api/fake/fake.go"
    line: 258
    severity: low
    message: |
      CreateSecret and Secret() copy the struct but share the Data map with the caller.
      A later mutation by the caller changes the stored Secret. Clone the map.
---
The REST client, types and fake match plan.md except for one missing field. Container lacks `RestartPolicy`, which the native sidecar in part B needs, so this needs changes.

## Checks
- Tests: `go test -race -cover ./internal/executor/kube/...` passes. Coverage is 86.3% for api and 89.5% for api/fake.
- Lint: `make lint` did not give a usable result. It printed module downloads and a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file the change did not touch. I did not rerun it.
- Diff coverage: I did not list the uncovered lines. All new functions are exercised by tests.
