package setupcmd

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
)

// The policy `ard setup` checks against. It is built in, never read from
// repository content or configuration: a pull request must not be able to
// weaken what checks it.
const (
	policyBranch     = "main"
	policyBypassMode = "always"
	policyVersioning = "ARD_VERSIONING"
	policyPRRule     = "pull_request"
)

// forbiddenAppPerms are App permissions the ard App must not have.
var forbiddenAppPerms = []string{"workflows", "administration"}

func rulesURL(r github.Repo) string { return "https://github.com/" + r.String() + "/settings/rules" }

func installURL(r github.Repo) string {
	return "https://github.com/" + r.String() + "/settings/installations"
}

// coversPolicyBranch reports whether a ruleset's ref conditions include main.
func coversPolicyBranch(rs github.Ruleset) bool {
	ref := "refs/heads/" + policyBranch
	for _, x := range rs.Exclude {
		if x == ref {
			return false
		}
	}
	for _, x := range rs.Branches {
		if x == ref || x == "~DEFAULT_BRANCH" || x == "~ALL" {
			return true
		}
	}
	return false
}

// checkRuleset checks that main's active rulesets require a pull request and
// that the ard App is the only bypass actor, with mode always.
func checkRuleset(repo github.Repo, rs []github.Ruleset, app github.AppInfo) Row {
	var problems []string
	hasPR := false
	appBypass := false
	for _, r := range rs {
		if r.Enforcement != "active" || r.Target != "branch" || !coversPolicyBranch(r) {
			continue
		}
		for _, t := range r.Rules {
			if t == policyPRRule {
				hasPR = true
			}
		}
		for _, b := range r.Bypass {
			isApp := b.ActorType == "Integration" && b.ActorID == app.ID
			switch {
			case isApp && b.Mode == policyBypassMode:
				appBypass = true
			case isApp:
				problems = append(problems, fmt.Sprintf("the App's bypass mode is %q in ruleset %q, want %q", b.Mode, r.Name, policyBypassMode))
			default:
				problems = append(problems, fmt.Sprintf("unexpected bypass actor %s %d (%s) in ruleset %q", b.ActorType, b.ActorID, b.Mode, r.Name))
			}
		}
	}
	if !hasPR {
		problems = append(problems, "no active ruleset on "+policyBranch+" requires a pull request")
	}
	if !appBypass {
		problems = append(problems, fmt.Sprintf("the ard App is not an %q bypass actor", policyBypassMode))
	}
	if len(problems) == 0 {
		return Row{Name: rulesetName, Status: StatusOK, Detail: policyBranch + " requires a pull request; only the ard App bypasses"}
	}
	return Row{Name: rulesetName, Status: StatusDrift, Detail: strings.Join(problems, "; ") + ". Fix in " + rulesURL(repo)}
}

// checkAppPerms checks the App holds none of the forbidden permissions.
func checkAppPerms(repo github.Repo, app github.AppInfo) Row {
	var bad []string
	for _, p := range forbiddenAppPerms {
		if lvl, ok := app.Permissions[p]; ok && lvl != "" {
			bad = append(bad, p)
		}
	}
	if len(bad) == 0 {
		return Row{Name: appPermName, Status: StatusOK, Detail: "no workflows or administration permission"}
	}
	return Row{Name: appPermName, Status: StatusDrift, Detail: "the App has the " + strings.Join(bad, ", ") + " permission; remove it in " + installURL(repo)}
}

// checkMerge checks that rebase merge is off when versioning is on.
func checkMerge(repo github.Repo, ms github.MergeSettings, versioning bool) Row {
	if versioning && ms.AllowRebaseMerge {
		return Row{Name: mergeName, Status: StatusDrift, Detail: fmt.Sprintf("allow_rebase_merge is on while %s is on; run: gh api -X PATCH repos/%s -F allow_rebase_merge=false", policyVersioning, repo)}
	}
	return Row{Name: mergeName, Status: StatusOK, Detail: "merge settings match the policy"}
}

func versioningOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}
