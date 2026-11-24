# Plan for #334

## Implementation

### Steps
1. In a scratch virtualenv outside the repo, find the smallest set of pins that resolves with pymdown-extensions at 11.0.1 or later. Run `pip install --dry-run "mkdocs==1.6.1" "mkdocs-material==<candidate>" "pymdown-extensions>=11.0.1"`. mkdocs-material 9.5.50 very likely requires pymdown-extensions~=10.2, so the pin alone will conflict. Try the newest mkdocs-material 9.x release first. This needs network access, so a worker with network does it, not triage.
2. Edit `docs/site/requirements.txt` (existing, 2 lines). Bump `mkdocs-material` to the release found in step 1 if one is needed. Add a new line `pymdown-extensions>=11.0.1`. The explicit floor makes the fix visible to the scanner, because the package is currently only a transitive dependency.
3. Run `make docs-check` (the existing target in `Makefile`). It runs `mkdocs build --strict -d /tmp/ard-site` and then `go test ./internal/docscheck/...`. `mkdocs.yml` enables no pymdownx extensions directly, so breakage is unlikely. Fix any strict-mode warning caused by the material bump only if it is trivial. Otherwise stop and report it.

### Test plan
- Case `docs build strict`: after `pip install -r docs/site/requirements.txt`, `mkdocs build --strict` exits 0.
- Case `installed version`: `pip show pymdown-extensions` reports 11.0.1 or later.
- Case `drift test`: `go test ./internal/docscheck/...` passes (file: `internal/docscheck`).
- Command: `make test` and `make docs-check`.
- Failure case: if pip reports a resolver conflict, pick a different mkdocs-material version. If no 9.x release allows pymdown-extensions 11, report the blocker.

### Risk
Low. Only docs-site dependencies change, and no protected path from merge.protected_paths is touched. The major-version jump of pymdown-extensions and a possible material bump could change docs rendering, so the strict build must pass. No runtime code is affected.

### Size
S. File touched: `docs/site/requirements.txt` (about 2 changed lines).
