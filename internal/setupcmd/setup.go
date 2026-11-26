package setupcmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"errors"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
)

// Opts are the flags of `ard setup`.
type Opts struct{ Check bool }

// PolicyReader is the read-only GitHub surface of the policy checks.
type PolicyReader interface {
	Identity(ctx context.Context) (github.Identity, error)
	ListRulesets(ctx context.Context) ([]github.Ruleset, error)
	GetMergeSettings(ctx context.Context) (github.MergeSettings, error)
	GetApp(ctx context.Context, slug string) (github.AppInfo, error)
	GetVariable(ctx context.Context, name string) (string, bool, error)
}

// Deps is what Run needs from the outside world.
type Deps struct {
	Reader      labelsync.Reader
	Writer      labelsync.Writer // the effect layer: a dry-run writer writes nothing
	Desired     []labels.Label
	Permissions func(ctx context.Context) Row
	LocalVsMain func(ctx context.Context) (warning string, err error)
	// Policy, when set, enables the ruleset, App permission and merge
	// setting checks for Repo. They only read.
	Policy PolicyReader
	Repo   github.Repo
	// Variables writes ARD_BOT_LOGIN with the operator's token; Confirm asks
	// before a different existing value is replaced (nil means no).
	Variables VariableWriter
	Confirm   func(prompt string) (bool, error)
	// WorkflowAt reads the bot-commits workflow on origin/main.
	WorkflowAt func(ctx context.Context) ([]byte, bool, error)
	DryRun     bool
	Log        *slog.Logger
}

const (
	permName  = "permissions"
	labelName = "labels"

	rulesetName = "ruleset"
	appPermName = "app-permissions"
	mergeName   = "merge-settings"
)

// Run executes the checks, then applies when the permission row is ok and
// Check is not set. The returned report holds the final rows.
func Run(ctx context.Context, d Deps, o Opts, out io.Writer) (Report, error) {
	if d.LocalVsMain != nil {
		w, err := d.LocalVsMain(ctx)
		if err != nil {
			w = "could not compare ./.ard/ with origin/main"
		}
		if w != "" {
			fmt.Fprintf(out, "warning: %s\n", w)
		}
	}
	perm := Row{Name: permName, Status: StatusOK}
	if d.Permissions != nil {
		perm = d.Permissions(ctx)
		perm.Name = permName
	}
	rep := Report{Rows: []Row{perm}}
	if perm.Status != StatusOK {
		// Nothing else can be read reliably without access.
		return rep, nil
	}
	if d.Policy != nil {
		rows, err := policyRows(ctx, d)
		if err != nil {
			return rep, err
		}
		rep.Rows = append(rep.Rows, rows...)
	}
	// Apply only when every check is green; label drift alone is fixable.
	// The workflow and bot login rows have their own fixes and do not block.
	blocked := !rep.Green()
	if d.WorkflowAt != nil {
		rep.Rows = append(rep.Rows, checkWorkflow(ctx, d.WorkflowAt))
	}
	if d.Policy != nil {
		b, err := checkBotLogin(ctx, d)
		if err != nil {
			return rep, err
		}
		if b.row.Status == StatusDrift && !o.Check && !blocked {
			b.row = applyBotLogin(ctx, d, b)
		}
		rep.Rows = append(rep.Rows, b.row)
	}
	have, err := d.Reader.ListLabels(ctx)
	if err != nil {
		return rep, fmt.Errorf("setup: list labels: %w", err)
	}
	plan := labelsync.Diff(have, d.Desired)
	row := Row{Name: labelName, Status: StatusOK}
	if n, m := len(plan.Create), len(plan.Update); n+m > 0 {
		row.Status = StatusDrift
		row.Detail = fmt.Sprintf("%d to create, %d to update", n, m)
	}
	if len(plan.Stale) > 0 {
		sep := ""
		if row.Detail != "" {
			sep = "; "
		}
		row.Detail += sep + "stale (not deleted): " + strings.Join(plan.Stale, ", ")
	}
	rep.Rows = append(rep.Rows, row)
	if o.Check || blocked || row.Status != StatusDrift {
		return rep, nil
	}

	sr, err := labelsync.Sync(ctx, d.Reader, d.Writer, d.Desired, d.Log)
	if err != nil && sr.Failed == nil {
		return rep, err
	}
	row = Row{Name: labelName, Status: StatusChanged}
	switch {
	case len(sr.Failed) > 0:
		var names []string
		for n := range sr.Failed {
			names = append(names, n)
		}
		sort.Strings(names)
		row = Row{Name: labelName, Status: StatusManual, Detail: "create or update these labels by hand: " + strings.Join(names, ", ")}
	case d.DryRun:
		row.Detail = fmt.Sprintf("would create %d, would update %d", len(sr.Create), len(sr.Update))
	default:
		row.Detail = fmt.Sprintf("created %d, updated %d", len(sr.Create), len(sr.Update))
	}
	rep.Rows[len(rep.Rows)-1] = row
	return rep, err
}

// policyRows runs the read-only policy checks. A forbidden or unknown-App
// read gives a manual row with the exact command to look it up by hand.
func policyRows(ctx context.Context, d Deps) ([]Row, error) {
	repo := d.Repo
	manual := func(name, detail string) Row { return Row{Name: name, Status: StatusManual, Detail: detail} }
	soft := func(err error) bool { return errors.Is(err, github.ErrForbidden) || errors.Is(err, github.ErrNotFound) }

	id, err := d.Policy.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("setup: identity: %w", err)
	}
	slug := strings.TrimSuffix(id.Login, "[bot]")
	var rows []Row

	app, appErr := d.Policy.GetApp(ctx, slug)
	if appErr != nil && !soft(appErr) {
		return nil, fmt.Errorf("setup: get app: %w", appErr)
	}
	appCmd := "gh api apps/" + slug
	rs, rsErr := d.Policy.ListRulesets(ctx)
	switch {
	case rsErr != nil && !soft(rsErr):
		return nil, fmt.Errorf("setup: list rulesets: %w", rsErr)
	case rsErr != nil:
		rows = append(rows, manual(rulesetName, "admin read is needed; check "+rulesURL(repo)+" or run: gh api repos/"+repo.String()+"/rulesets"))
	case appErr != nil:
		rows = append(rows, manual(rulesetName, "cannot resolve the App; run: "+appCmd))
	default:
		rows = append(rows, checkRuleset(repo, rs, app))
	}
	if appErr != nil {
		rows = append(rows, manual(appPermName, "cannot read the App; run: "+appCmd+" and check the permissions at "+installURL(repo)))
	} else {
		rows = append(rows, checkAppPerms(repo, app))
	}

	ms, err := d.Policy.GetMergeSettings(ctx)
	if err != nil {
		if !soft(err) {
			return nil, fmt.Errorf("setup: merge settings: %w", err)
		}
		return append(rows, manual(mergeName, "cannot read merge settings; run: gh api repos/"+repo.String())), nil
	}
	v, found, err := d.Policy.GetVariable(ctx, policyVersioning)
	if err != nil {
		if !soft(err) {
			return nil, fmt.Errorf("setup: read %s: %w", policyVersioning, err)
		}
		return append(rows, manual(mergeName, fmt.Sprintf("cannot read the %s variable; run: gh variable get %s -R %s", policyVersioning, policyVersioning, repo))), nil
	}
	return append(rows, checkMerge(repo, ms, found && versioningOn(v))), nil
}
