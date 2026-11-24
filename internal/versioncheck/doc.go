// Package versioncheck only tests the version workflow. The logic is the pair
// of inline scripts in steps "plan" and "tag" of .github/workflows/version.yml;
// the tests extract them from the YAML and run them against throwaway bare
// repositories and a fake gh.
package versioncheck
