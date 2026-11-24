# Plan for #336

## Implementation

### Steps
1. Edit `docs/site/requirements.txt`: append, following the existing certifi entry:
   ```
   # idna: GHSA-65pc-fj4g-8rjx fixed in 3.15
   idna>=3.15
   ```
   idna is only a transitive dependency of the mkdocs toolchain (via requests), so a lower bound forces the resolver to pick the fixed release without pinning it exactly.

### Test plan
- Resolve check (needs network, in a throwaway venv outside the repo): `pip install --dry-run -r docs/site/requirements.txt` should select idna >= 3.15 with no conflict.
- Optional: `mkdocs build` from `docs/site` requirements still succeeds.
- Failure case: if resolution conflicts, report it rather than loosening the constraint.
- Go tests (`make test`) are unaffected.

### Risk
Low. Docs tooling only, not the Go binary. No protected path from merge.protected_paths is touched.

### Size
S. One file touched: `docs/site/requirements.txt` (about 2 lines).
