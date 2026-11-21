// Package botcheck only tests the bot-commit audit. The check itself is the
// inline script in step "check" of .github/workflows/bot-commits.yml; the tests
// extract it from the YAML and run it against throwaway git repositories.
package botcheck
