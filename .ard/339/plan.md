# Plan for #339

## Implementation

### Steps
1. Edit `docs/site/requirements.txt`: append a comment line `# pymdown-extensions: GHSA-r6h4-mm7h-8pmq fixed in 10.16.1` and the line `pymdown-extensions>=10.16.1`, following the existing certifi pin style. Reason: pymdown-extensions is a transitive dependency of the pinned `mkdocs-material==9.5.50`, which requires `pymdown-extensions~=10.2` (so 10.16.x is allowed). A floor forces the fixed release.
2. Check the pin: in a throwaway virtualenv outside the repo, run `pip install -r docs/site/requirements.txt` and confirm pip resolves without conflicts and installs 10.16.1 or later below 11.

### Test plan
- Resolution: `pip install -r docs/site/requirements.txt` in a fresh venv succeeds; `pip show pymdown-extensions` reports >=10.16.1 and <11.
- Build: `mkdocs build --strict` against the docs site config succeeds with no new warnings. Find the config file location in the repo (mkdocs.yml) before running.
- Failure case: if resolution conflicts, report it and do not loosen the mkdocs-material pin without a maintainer.

### Risk
Low. Docs toolchain only, no shipped code. No protected paths touched. Minor-version jump within major 10.

### Size
S. Files: `docs/site/requirements.txt` (about 2 lines).
