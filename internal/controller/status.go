package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

// Status issue identity. The marker, not the title, is what makes an issue
// ours; the author check stops a stranger's look-alike being adopted.
const (
	StatusTitle  = "ard status"
	StatusMarker = "<!-- ard:status -->"
)

// StatusReader is the search the lookup needs.
type StatusReader interface {
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
}

// StatusEffects is the write side, satisfied by effects.Effects.
type StatusEffects interface {
	CreateIssue(ctx context.Context, in github.NewIssue) (github.Issue, error)
	PinIssue(ctx context.Context, n int) error
}

const statusBody = StatusMarker + "\n\nManaged by ard. Add the `" + labels.Pause +
	"` label to stop new claims; remove it to resume.\n"

// EnsureStatusIssue returns the number of the pinned status issue, creating
// and pinning it when none exists. author is the login ard acts as. Under
// dry-run the effect layer returns a negative synthetic number, which is
// returned as is. A pin failure is a warning, not an error. The lookup uses
// search, which is eventually consistent; if two issues match the lowest
// number wins, so a duplicate created by a quick restart is self-healing.
func EnsureStatusIssue(ctx context.Context, gh StatusReader, fx StatusEffects, author string, log *slog.Logger) (int, error) {
	if log == nil {
		log = slog.Default()
	}
	q := fmt.Sprintf(`is:issue is:open in:title "%s"`, StatusTitle)
	found, err := gh.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc"})
	if err != nil {
		return 0, fmt.Errorf("find status issue: %w", err)
	}
	var nums []int
	for _, is := range found {
		if is.IsPR || is.Title != StatusTitle || !strings.EqualFold(is.Author, author) ||
			!strings.Contains(is.Body, StatusMarker) {
			continue
		}
		nums = append(nums, is.Number)
	}
	sort.Ints(nums)
	switch {
	case len(nums) == 1:
		return nums[0], nil
	case len(nums) > 1:
		log.Warn("status.duplicate", "using", nums[0], "all", nums)
		return nums[0], nil
	}
	is, err := fx.CreateIssue(ctx, github.NewIssue{Title: StatusTitle, Body: statusBody})
	if err != nil {
		return 0, fmt.Errorf("create status issue: %w", err)
	}
	if err := fx.PinIssue(ctx, is.Number); err != nil {
		log.Warn("status.pin_failed", "issue", is.Number, "error", err,
			"hint", "pin the status issue manually")
	}
	log.Info("status.created", "issue", is.Number)
	return is.Number, nil
}
