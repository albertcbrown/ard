package initcmd

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
)

// initBody is the PR description. It is fixed text built from the file list
// and the validated protected paths, with no issue or comment text in it.
func initBody(pl Result, res *config.Resolved, kind github.AuthKind) string {
	var b strings.Builder
	b.WriteString("This PR adds the starter `.ard/` directory proposed by `ard init`.\n\n")
	b.WriteString("## Files\n\n")
	ops := pl.Ops
	for _, o := range ops {
		if o.Act == actUnchanged || o.Act == actEdited {
			continue
		}
		mode := ""
		if o.File.Exec {
			mode = " (executable)"
		}
		fmt.Fprintf(&b, "- %s `%s`%s\n", o.Act, o.File.Path, mode)
	}
	fmt.Fprintf(&b, "- add or update `%s`, the hashes of the starter files written\n", LockPath)
	var edited []string
	for _, o := range ops {
		if o.Act == actEdited {
			edited = append(edited, o.File.Path)
		}
	}
	if len(edited) > 0 {
		b.WriteString("\n## Edited files left alone\n\nThese differ from both the starter and the last `ard init`, so they are not changed. Run `ard init` locally to see the diffs.\n\n")
		for _, p := range edited {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	if len(pl.Dropped) > 0 {
		b.WriteString("\n## No longer in the starter\n\nLeft in place; delete by hand if unwanted.\n\n")
		for _, p := range pl.Dropped {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	b.WriteString("\n## Protected paths\n\nA pull request touching any of these waits for a human approval:\n\n")
	for _, p := range res.ProtectedPaths {
		fmt.Fprintf(&b, "- `%s`\n", p)
	}
	b.WriteString("\n## Before you merge\n\n")
	b.WriteString("- Nothing takes effect until this is merged: ard serve reads its configuration from origin/main.\n")
	b.WriteString("- Merge with **Squash and merge** (recommended); **Create a merge commit** is also safe. Never fast-forward this branch into main from the command line: the bot-commit check reads main's first-parent history and would fail on the bot's commit.\n")
	b.WriteString("- If this repository already has an `.ard/config.yaml`, this PR does not edit it: add `.ard/PRODUCT.md`, `.ard/VISION.md` and `.ard/TECH_STACK.md` to `merge.protected_paths` yourself, so a PR cannot change them without a human approval.\n")
	for _, o := range ops {
		if o.File.Path == ".ard/scanners/lint/scan" {
			b.WriteString("- The lint scanner fails until you commit `.ard/scanners/lint/baseline`: run the command in `.ard/scanners/lint/Dockerfile`'s header (an empty file means no baseline).\n")
			break
		}
	}
	b.WriteString("- `scanners/trufflehog` is included. It files leaked-credential issues for a human (never for an agent) and needs a TruffleHog binary (AGPL-3.0; it is installed by the role's Dockerfile or taken from the host, and is not in the ard image). To opt out, delete `.ard/scanners/trufflehog/` and its `scanners/trufflehog` line under `overrides:` in `.ard/config.yaml` before merging.\n")
	b.WriteString("- `scanners/trufflehog-history` is included. It scans the full git history weekly for leaked credentials, with the same fingerprints as `scanners/trufflehog`, and its issues never autoclose. It reads the controller's clone through a read-only mount and needs the same TruffleHog binary. To opt out, delete `.ard/scanners/trufflehog-history/` and its `scanners/trufflehog-history` line under `overrides:` in `.ard/config.yaml` before merging.\n")
	b.WriteString("\n## Follow-up ard does not do\n\n")
	b.WriteString("ard never writes `.github/`, so do this by hand")
	step2 := "Set the repository variable `ARD_BOT_LOGIN` to the App's `<slug>[bot]` login."
	if kind == github.AuthToken {
		b.WriteString(" (this PR was opened with a personal access token, which may be able to write `.github/`, but ard still never does):\n\n")
		step2 = "Set the repository variable `ARD_BOT_LOGIN` to the login of the token's user. The bot-commit check cannot tell ard's main-lane commits from that user's own commits."
	} else {
		b.WriteString(": ard's GitHub App has no permission to write it.\n\n")
	}
	b.WriteString("1. Copy `.github/workflows/bot-commits.yml` from the ard repository into this repository.\n")
	b.WriteString("2. " + step2 + "\n")
	b.WriteString("3. Optionally set the repository variable `ARD_BOT_CHECK_SINCE`.\n")
	return b.String()
}
