# Plan for #333

## Implementation

### Steps
1. Edit `docs/site/requirements.txt`: append a line `certifi>=2026.10.18` (new). certifi is only a transitive dependency (via mkdocs-material's requests), so an explicit lower bound makes pip resolve the fixed release. Keep the existing `mkdocs==1.6.1` and `mkdocs-material==9.5.50` pins unchanged.
2. Optionally add a one-line comment above it noting the GHSA-6q48-6c7w-9gw2 reason, so the pin is not removed later.

### Test plan
- In a fresh virtualenv: `pip install -r docs/site/requirements.txt` then `pip show certifi` must report version 2026.10.18 or newer.
- `make docs-check` (runs `mkdocs build --strict -d /tmp/ard-site` and `go test ./internal/docscheck/...`) must pass.
- Failure case: if pip reports a resolution conflict, raise the mkdocs-material pin instead.
- Re-running the cve scanner should tick the finding.

### Risk
Low. Docs-only build dependency; `docs/site/requirements.txt` is not in merge.protected_paths. No public interface, migration or concurrency impact.

### Size
S. Files touched: `docs/site/requirements.txt`.
