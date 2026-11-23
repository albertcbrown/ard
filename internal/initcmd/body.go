package initcmd

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/config"
)

// initBody is the PR description. It is fixed text built from the file list
// and the validated protected paths, with no issue or comment text in it.
func initBody(ops []op, res *config.Resolved) string {
	var b strings.Builder
	b.WriteString("This PR adds the starter `.ard/` directory proposed by `ard init`.\n\n")
	b.WriteString("## Files\n\n")
	for _, o := range ops {
		if o.Act == actUnchanged {
			continue
		}
		mode := ""
		if o.File.Exec {
			mode = " (executable)"
		}
		fmt.Fprintf(&b, "- %s `%s`%s\n", o.Act, o.File.Path, mode)
	}
	b.WriteString("\n## Protected paths\n\nA pull request touching any of these waits for a human approval:\n\n")
	for _, p := range res.ProtectedPaths {
		fmt.Fprintf(&b, "- `%s`\n", p)
	}
	b.WriteString("\n## Before you merge\n\n")
	b.WriteString("- Nothing takes effect until this is merged: ard serve reads its configuration from origin/main.\n")
	b.WriteString("- Merge with **Squash and merge** (recommended); **Create a merge commit** is also safe. Never fast-forward this branch into main from the command line: the bot-commit check reads main's first-parent history and would fail on the bot's commit.\n")
	for _, o := range ops {
		if o.File.Path == ".ard/scanners/lint/scan" {
			b.WriteString("- The lint scanner fails until you commit `.ard/scanners/lint/baseline`: run the command in `.ard/scanners/lint/Dockerfile`'s header (an empty file means no baseline).\n")
			break
		}
	}
	b.WriteString("\n## Follow-up the App cannot do\n\n")
	b.WriteString("ard's GitHub App has no permission to write `.github/`, so do this by hand:\n\n")
	b.WriteString("1. Copy `.github/workflows/bot-commits.yml` from the ard repository into this repository.\n")
	b.WriteString("2. Set the repository variable `ARD_BOT_LOGIN` to the App's `<slug>[bot]` login.\n")
	b.WriteString("3. Optionally set the repository variable `ARD_BOT_CHECK_SINCE`.\n")
	return b.String()
}
