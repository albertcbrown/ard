// Package limits is the controller's book of exhausted usage limits: the
// reactive half of subscription limit handling (#81). When a job stops at a
// limit, the Reaper's Finisher records it here and the pool starts no agent
// job for the exhausted account or model family until it resets. Observed
// readings, thresholds and status comments are #63's.
//
// The book is persisted in the controller database's meta table, so a pause
// survives a restart. It never kills a running job.
package limits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/state"
)

const (
	// DefaultPause is the first pause when the CLI gave no reset time.
	DefaultPause = 30 * time.Minute
	// BackoffCap is the longest pause chosen without a reset time. Each
	// further stop with no reset doubles the pause up to this; a job that
	// succeeds resets it.
	BackoffCap = 4 * time.Hour
	// MaxPause clamps a reset time that is far away: a misread unit must not
	// block the account for years.
	MaxPause = 8 * 24 * time.Hour

	metaKey = "limits.exhausted"
)

// Meta is the key-value store the book persists in (*state.Store).
type Meta interface {
	Meta(ctx context.Context, key string) (string, error) // errors.Is(err, state.ErrNotFound) = empty book
	SetMeta(ctx context.Context, key, value string) error
}

// entry is one exhausted family. Family "" is the whole account.
type entry struct {
	Until int64 `json:"until"`          // Unix seconds
	Step  int   `json:"step,omitempty"` // consecutive stops with no reset time
}

// Book records which families are exhausted and until when.
type Book struct {
	mu       sync.Mutex
	entries  map[string]entry
	readings map[string]Reading
	meta     Meta
	now      func() time.Time
	log      *slog.Logger
}

// Open loads the book. A missing key gives an empty book; a malformed value
// logs WARN limits.corrupt and gives an empty book (the next Exhaust
// overwrites it). Only a read error from meta is returned.
func Open(ctx context.Context, m Meta, now func() time.Time, log *slog.Logger) (*Book, error) {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	b := &Book{entries: map[string]entry{}, readings: map[string]Reading{}, meta: m, now: now, log: log}
	v, err := m.Meta(ctx, metaKey)
	switch {
	case errors.Is(err, state.ErrNotFound):
	case err != nil:
		return nil, fmt.Errorf("open limits book: %w", err)
	default:
		var got map[string]entry
		if err := json.Unmarshal([]byte(v), &got); err != nil {
			log.Warn("limits.corrupt", "error", err)
		} else if got != nil {
			b.entries = got
		}
	}
	if err := b.loadReadings(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func backoff(step int) time.Duration {
	d := DefaultPause
	for i := 0; i < step && d < BackoffCap; i++ {
		d *= 2
	}
	return min(d, BackoffCap)
}

// Exhaust records that family (or the whole account when "") is out of quota
// and returns the effective time it is blocked until.
//
// A resetsAt in the future is used as given, clamped to MaxPause. A zero or
// past resetsAt means the CLI gave no reset: the pause is DefaultPause, then
// doubles with each further stop to BackoffCap, but only once the previous
// pause has run out. A stop recorded while the family is already paused (a job
// that was running when the limit hit) changes nothing, so Exhaust is
// idempotent. The later of the old and new times is kept, and the book is
// persisted before Exhaust returns; on a persist error memory is unchanged.
func (b *Book) Exhaust(ctx context.Context, family string, resetsAt time.Time) (time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	old, had := b.entries[family]
	oldUntil := time.Unix(old.Until, 0)
	next := old
	var until time.Time
	switch {
	case resetsAt.After(now):
		until = resetsAt
		if limit := now.Add(MaxPause); until.After(limit) {
			until = limit
		}
		next.Step = 0
	case had && now.Before(oldUntil):
		until = oldUntil
	default:
		until = now.Add(backoff(old.Step))
		next.Step = old.Step + 1
		b.log.Info("limits.backoff", "family", family, "step", next.Step, "pause", backoff(old.Step).String(), "until", until.UTC())
	}
	if had && until.Before(oldUntil) {
		until, next = oldUntil, old
	}
	next.Until = until.Unix()
	updated := b.pruned(now)
	updated[family] = next
	if err := b.persist(ctx, updated); err != nil {
		return time.Time{}, err
	}
	b.entries = updated
	if next.Until != old.Until || !had {
		b.log.Info("limits.exhausted", "family", family, "until", until.UTC())
	}
	return time.Unix(next.Until, 0).UTC(), nil
}

// Succeeded resets the backoff of family and the whole account after a job
// ended successfully. It leaves the blocked-until times alone.
func (b *Book) Succeeded(ctx context.Context, family string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	updated := b.pruned(b.now())
	changed := false
	for k, e := range updated {
		if e.Step != 0 && (family == "" || k == "" || k == family) {
			e.Step = 0
			updated[k] = e
			changed = true
		}
	}
	if !changed {
		return nil
	}
	updated = pruneExpired(updated, b.now())
	if err := b.persist(ctx, updated); err != nil {
		return err
	}
	b.entries = updated
	return nil
}

// pruned copies the entries, dropping those that have expired with no backoff
// to remember.
func (b *Book) pruned(now time.Time) map[string]entry {
	return pruneExpired(b.entries, now)
}

func pruneExpired(in map[string]entry, now time.Time) map[string]entry {
	out := make(map[string]entry, len(in)+1)
	for k, e := range in {
		if e.Step > 0 || now.Before(time.Unix(e.Until, 0)) {
			out[k] = e
		}
	}
	return out
}

func (b *Book) persist(ctx context.Context, m map[string]entry) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode limits book: %w", err)
	}
	if err := b.meta.SetMeta(ctx, metaKey, string(raw)); err != nil {
		return fmt.Errorf("persist limits book: %w", err)
	}
	return nil
}

// Until returns the time a role on family may next start, or zero when it may
// start now. It is the later of the whole-account entry and the family's own.
// An unknown family ("", for example a role on the CLI default model) is
// blocked by any entry, so it fails closed. Blocked means now is before the
// returned time; at that time the role is free.
func (b *Book) Until(family string) time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	var latest time.Time
	for k, e := range b.entries {
		if family != "" && k != "" && k != family {
			continue
		}
		if t := time.Unix(e.Until, 0); now.Before(t) && t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return latest
	}
	return latest.UTC()
}

// FamilyOf maps a model name to its limit family: "opus", "sonnet" or
// "haiku" by case-insensitive substring, otherwise "".
func FamilyOf(model string) string {
	m := strings.ToLower(model)
	for _, f := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(m, f) {
			return f
		}
	}
	return ""
}
