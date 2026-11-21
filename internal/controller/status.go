package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// Status issue identity. The marker, not the title, is what makes an issue
// ours; the author check stops a stranger's look-alike being adopted.
const (
	StatusTitle  = "ard status"
	StatusMarker = "<!-- ard:status -->"
)

// StatusIssueKey is the meta key that remembers the status issue number.
const StatusIssueKey = "status_issue"

// StatusReader is what the lookup reads: the cached issue directly, else search.
type StatusReader interface {
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
}

// StatusCache remembers the status issue number across restarts. *state.Store
// satisfies it. Meta returns an error wrapping state.ErrNotFound when unset.
type StatusCache interface {
	Meta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

var _ StatusCache = (*state.Store)(nil)

// StatusEffects is the write side, satisfied by effects.Effects.
type StatusEffects interface {
	CreateIssue(ctx context.Context, in github.NewIssue) (github.Issue, error)
	PinIssue(ctx context.Context, n int) error
}

const statusBody = StatusMarker + "\n\nManaged by ard. Add the `" + labels.Pause +
	"` label to stop new claims; remove it to resume.\n"

// ownStatusIssue reports whether is is an open status issue ard created.
func ownStatusIssue(is github.Issue, author string) bool {
	return !is.IsPR && is.State == github.StateOpen && is.Title == StatusTitle &&
		strings.EqualFold(is.Author, author) && strings.Contains(is.Body, StatusMarker)
}

// EnsureStatusIssue returns the number of the pinned status issue, creating
// and pinning it when none exists. author is the login ard acts as. The number
// cached under StatusIssueKey is tried first and adopted if that issue is
// still our open status issue; otherwise (or when it cannot be read) search is
// the fallback, then create. Search is eventually consistent, so the cache is
// what stops a quick restart creating a second issue; the lowest-number rule
// only covers a crash between create and remember, or a lost cache. cache may
// be nil. Under dry-run the effect layer returns a negative synthetic number,
// which is returned as is and never cached. A pin failure is a warning.
func EnsureStatusIssue(ctx context.Context, gh StatusReader, fx StatusEffects, cache StatusCache, author string, log *slog.Logger) (int, error) {
	if log == nil {
		log = slog.Default()
	}
	prev := 0
	if cache != nil {
		prev = cachedStatusNumber(ctx, cache, log)
	}
	if prev > 0 {
		n, ok := adoptCached(ctx, gh, prev, author, log)
		if ok {
			return n, nil
		}
	}
	q := fmt.Sprintf(`is:issue is:open in:title "%s"`, StatusTitle)
	found, err := gh.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc"})
	if err != nil {
		return 0, fmt.Errorf("find status issue: %w", err)
	}
	var nums []int
	for _, is := range found {
		if ownStatusIssue(is, author) {
			nums = append(nums, is.Number)
		}
	}
	sort.Ints(nums)
	switch {
	case len(nums) == 1:
		remember(ctx, cache, nums[0], prev, log)
		return nums[0], nil
	case len(nums) > 1:
		log.Warn("status.duplicate", "using", nums[0], "all", nums)
		remember(ctx, cache, nums[0], prev, log)
		return nums[0], nil
	}
	is, err := fx.CreateIssue(ctx, github.NewIssue{Title: StatusTitle, Body: statusBody})
	if err != nil {
		return 0, fmt.Errorf("create status issue: %w", err)
	}
	// Before pinning, to keep the window where a crash loses the number short.
	remember(ctx, cache, is.Number, prev, log)
	if err := fx.PinIssue(ctx, is.Number); err != nil {
		log.Warn("status.pin_failed", "issue", is.Number, "error", err,
			"hint", "pin the status issue manually")
	}
	log.Info("status.created", "issue", is.Number)
	return is.Number, nil
}

// cachedStatusNumber returns the cached issue number, or 0 when none is usable.
func cachedStatusNumber(ctx context.Context, cache StatusCache, log *slog.Logger) int {
	v, err := cache.Meta(ctx, StatusIssueKey)
	if errors.Is(err, state.ErrNotFound) {
		return 0
	}
	if err != nil {
		log.Warn("status.cache_read_failed", "error", err)
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Warn("status.cache_invalid", "value", v)
		return 0
	}
	return n
}

// adoptCached reads the cached issue and reports whether it is still ours. Any
// failure to adopt falls back to search; a read error other than not-found
// does too, since starting over beats aborting.
func adoptCached(ctx context.Context, gh StatusReader, n int, author string, log *slog.Logger) (int, bool) {
	is, err := gh.GetIssue(ctx, n)
	switch {
	case errors.Is(err, github.ErrNotFound):
		log.Info("status.cache_stale", "issue", n, "reason", "not_found")
		return 0, false
	case err != nil:
		log.Warn("status.cache_unreadable", "issue", n, "error", err)
		return 0, false
	}
	if is.Number == n && ownStatusIssue(is, author) {
		return n, true
	}
	reason := "foreign"
	if is.State == github.StateClosed {
		reason = "closed"
	}
	log.Info("status.cache_stale", "issue", n, "reason", reason)
	return 0, false
}

// remember caches n unless there is no cache, n is a dry-run synthetic number
// or the cache already holds it. A write failure is a warning: the issue exists.
func remember(ctx context.Context, cache StatusCache, n, prev int, log *slog.Logger) {
	if cache == nil || n <= 0 || n == prev {
		return
	}
	if err := cache.SetMeta(ctx, StatusIssueKey, strconv.Itoa(n)); err != nil {
		log.Warn("status.cache_write_failed", "issue", n, "error", err)
	}
}
