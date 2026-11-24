# Plan for #337

## Implementation

### Steps
1. Edit `docs/site/requirements.txt`: add a direct constraint below the certifi entry, in the same style, e.g. a comment `# pymdown-extensions: GHSA-9xwg-3r6f-jcx2 fixed in 11.0.0` and the line `pymdown-extensions>=11.0.0`. This makes the resolver choose the fixed release.
2. Check that the resolver accepts it with a dry run in a fresh venv outside the repo: `pip install --dry-run -r docs/site/requirements.txt`. mkdocs-material 9.5.50 likely declares `pymdown-extensions~=10.2`, which would conflict. If it does, raise the `mkdocs-material==` pin to the lowest release whose requirement allows pymdown-extensions 11.x, and keep `mkdocs==1.6.1` unless that release needs a newer one. Pin exactly, as the file already does.
3. Build the docs with the new pins in the venv: `mkdocs build --strict` run against the repo's mkdocs config. Look for it with `git ls-files | grep mkdocs`, because I did not find it under docs/site. Fix nothing else; if the build breaks on the new major version, stop and report.

### Test plan
- Resolve: the dry run in step 2 exits 0 and reports pymdown-extensions >= 11.0.0.
- Build: `mkdocs build --strict` succeeds, with no new warnings compared with the pre-change build.
- Failure case: if no mkdocs-material release allows pymdown-extensions 11, mark the work blocked and report this on the issue.
- Repository command: `make test`. It does not exercise the docs build, so the build check above is needed.
- Edge case: confirm certifi's constraint is unchanged.

### Risk
Low to medium. Only the docs build dependencies change, and no runtime code. The pymdown-extensions major bump and a possible mkdocs-material bump can alter rendering or break the build. No protected paths from merge.protected_paths are touched. The b64 extension does not appear to be enabled, so the vulnerable code is probably unreachable.

### Size
S. File touched: `docs/site/requirements.txt` (about 3 to 5 changed lines).
