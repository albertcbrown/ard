# Plan for #338

## Implementation

### Steps
1. Edit `docs/site/requirements.txt`: change `mkdocs-material==9.5.50` to `mkdocs-material==9.7.7` (the advisory's fixed release). Leave `mkdocs==1.6.1` and the `certifi>=2026.10.18` lines and its comment unchanged unless the 9.7.7 package metadata requires a newer mkdocs; if it does, bump the `mkdocs` pin to the minimum compatible version.
2. Verify the pin resolves: in a throwaway virtualenv outside the repo, run `pip install -r docs/site/requirements.txt`, then `make docs-check` (runs `mkdocs build --strict -d /tmp/ard-site` and `go test ./internal/docscheck/...`). `mkdocs.yml` sets `strict: true` and uses `search.suggest` (the feature the advisory concerns), so new warnings fail the build. If the sandbox has no network, say so and rely on CI.
3. If strict build breaks on a renamed or removed theme option in `mkdocs.yml`, make the smallest fix there; otherwise leave it alone.

### Test plan
- Build: `make docs-check` must pass with no warnings (strict mode).
- Case: `mkdocs build --strict` with 9.7.7 and mkdocs 1.6.1 succeeds; expected exit 0.
- Case: `go test ./internal/docscheck/...` still passes (docs drift test); no code touched.
- Failure case: if pip reports a mkdocs version conflict, raise the mkdocs pin as in step 1.
- Check `grep -rn "9.5.50" --exclude-dir=.git --exclude-dir=.ard .` finds no stale references.

### Risk
Low. Docs-only build dependency, no shipped code. It is a minor-version jump (9.5 to 9.7), so theme behavior could shift slightly; strict mode will catch config problems. No protected paths from merge.protected_paths are touched (`.github/workflows/` is not changed; CI picks up the file through pip).

### Size
S. Files touched: `docs/site/requirements.txt` (one line), possibly `mkdocs.yml`.
