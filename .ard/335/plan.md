# Plan for #335

## Implementation

### Steps
1. Edit `docs/site/requirements.txt`: append, following the existing certifi convention, a comment line `# pymdown-extensions: GHSA-62q4-447f-wv8h fixed in 10.21.3` and the line `pymdown-extensions>=10.21.3`. Reason: pymdown-extensions arrives transitively via mkdocs-material, so an explicit floor is the smallest fix. Raising mkdocs-material is not needed: mkdocs-material 9.5.x declares `pymdown-extensions~=10.2`, which allows 10.21.3 (same major).
2. Do not change the mkdocs or mkdocs-material pins.

### Test plan
- Install in a fresh venv: `pip install -r docs/site/requirements.txt`. Expect no resolver conflict, and `pip show pymdown-extensions` reports >= 10.21.3.
- Build the site from the repo's mkdocs config (find it with `git ls-files | grep mkdocs.yml`; if none exists, check the docs workflow for the build command): `mkdocs build --strict`. Expect success.
- Edge case: if the docs use the snippets extension, check that includes still resolve. If the new restrict_base_path behaviour breaks a path, fix the path.
- Failure case: if the resolver conflicts, raise mkdocs-material to the lowest 9.x release that allows the fix and re-run the build.

### Risk
Low. Docs-build dependency only, no shipped code. No protected paths touched. Several minor releases are skipped, so the docs build check matters.

### Size
S. Files: `docs/site/requirements.txt` (about 2 added lines).
