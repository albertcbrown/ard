package setupcmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
)

// Opts are the flags of `ard setup`.
type Opts struct{ Check bool }

// Deps is what Run needs from the outside world.
type Deps struct {
	Reader      labelsync.Reader
	Writer      labelsync.Writer // the effect layer: a dry-run writer writes nothing
	Desired     []labels.Label
	Permissions func(ctx context.Context) Row
	LocalVsMain func(ctx context.Context) (warning string, err error)
	DryRun      bool
	Log         *slog.Logger
}

const (
	permName  = "permissions"
	labelName = "labels"
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
	if o.Check || row.Status != StatusDrift {
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
