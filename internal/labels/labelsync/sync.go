// Package labelsync makes a repository's ard: labels match the catalogue
// built by package labels. It reads through a narrow Reader and writes only
// through the effect layer, so --dry-run logs the plan and changes nothing.
// It creates and updates labels and never deletes any.
package labelsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

// Reader lists the repository's labels. github.Reader satisfies it.
type Reader interface {
	ListLabels(ctx context.Context) ([]github.Label, error)
}

// Writer creates and updates label definitions. effects.Effects satisfies it.
type Writer interface {
	CreateLabel(ctx context.Context, l github.Label) error
	UpdateLabel(ctx context.Context, name string, l github.Label) error
}

// Update is a label that exists but differs from the catalogue.
type Update struct {
	Have github.Label
	Want labels.Label
}

// Plan is the difference between the repository and the catalogue.
type Plan struct {
	Create    []labels.Label
	Update    []Update
	Unchanged []string
	Stale     []string // ard: labels on the repo that are not in the catalogue, sorted
}

// Diff compares have with want. GitHub matches label names
// case-insensitively, so a name differing only in case is unchanged. Labels
// without the ard: prefix are ignored.
func Diff(have []github.Label, want []labels.Label) Plan {
	idx := make(map[string]github.Label, len(have))
	for _, h := range have {
		idx[strings.ToLower(h.Name)] = h
	}
	var p Plan
	wanted := make(map[string]bool, len(want))
	for _, w := range want {
		k := strings.ToLower(w.Name)
		wanted[k] = true
		h, ok := idx[k]
		switch {
		case !ok:
			p.Create = append(p.Create, w)
		case !strings.EqualFold(strings.TrimPrefix(h.Color, "#"), strings.TrimPrefix(w.Color, "#")) ||
			h.Description != w.Description:
			p.Update = append(p.Update, Update{Have: h, Want: w})
		default:
			p.Unchanged = append(p.Unchanged, w.Name)
		}
	}
	for _, h := range have {
		k := strings.ToLower(h.Name)
		if strings.HasPrefix(k, labels.Prefix) && !wanted[k] {
			p.Stale = append(p.Stale, h.Name)
		}
	}
	sort.Slice(p.Create, func(i, j int) bool { return p.Create[i].Name < p.Create[j].Name })
	sort.Slice(p.Update, func(i, j int) bool { return p.Update[i].Want.Name < p.Update[j].Want.Name })
	sort.Strings(p.Unchanged)
	sort.Strings(p.Stale)
	return p
}

// Report is the outcome of Sync.
type Report struct {
	Plan
	Failed map[string]error // label name -> error from the write
}

// Sync lists the repository's labels, diffs them against want and applies the
// difference: creates first, then updates, each in name order. One failure
// does not stop the rest; Sync returns the failures joined. A list error
// returns before anything is written. Stale labels are logged, never deleted.
func Sync(ctx context.Context, r Reader, w Writer, want []labels.Label, log *slog.Logger) (Report, error) {
	if log == nil {
		log = slog.Default()
	}
	have, err := r.ListLabels(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("labels sync: list labels: %w", err)
	}
	rep := Report{Plan: Diff(have, want), Failed: map[string]error{}}
	var errs []error
	fail := func(name string, err error) {
		rep.Failed[name] = err
		errs = append(errs, fmt.Errorf("labels sync: %s: %w", name, err))
	}
	for _, l := range rep.Create {
		log.InfoContext(ctx, "label", "label", l.Name, "action", "create")
		if err := w.CreateLabel(ctx, github.Label{Name: l.Name, Color: l.Color, Description: l.Description}); err != nil {
			fail(l.Name, err)
		}
	}
	for _, u := range rep.Update {
		log.InfoContext(ctx, "label", "label", u.Want.Name, "action", "update",
			"old_color", u.Have.Color, "new_color", u.Want.Color,
			"old_desc", u.Have.Description, "new_desc", u.Want.Description)
		// Keep the repo's own spelling of the name: no rename.
		err := w.UpdateLabel(ctx, u.Have.Name, github.Label{Name: u.Have.Name, Color: u.Want.Color, Description: u.Want.Description})
		if err != nil {
			fail(u.Want.Name, err)
		}
	}
	for _, s := range rep.Stale {
		log.WarnContext(ctx, "stale label", "label", s, "hint", "not deleted; remove it by hand if unused")
	}
	log.InfoContext(ctx, "labels sync",
		"created", len(rep.Create), "updated", len(rep.Update), "unchanged", len(rep.Unchanged), "stale", len(rep.Stale), "failed", len(rep.Failed))
	return rep, errors.Join(errs...)
}
