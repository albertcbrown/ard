package worker

import "strings"

// CommitType maps an issue's type:* label to a conventional-commit type:
// type:feature is feat, type:bug is fix, and docs, chore, refactor and test
// keep their names. Any other label, or none, is chore.
func CommitType(labels []string) string {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "type:feature":
			return "feat"
		case "type:bug":
			return "fix"
		case "type:docs":
			return "docs"
		case "type:chore":
			return "chore"
		case "type:refactor":
			return "refactor"
		case "type:test":
			return "test"
		}
	}
	return "chore"
}
