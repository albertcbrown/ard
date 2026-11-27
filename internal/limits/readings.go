package limits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/state"
)

const readingsKey = "limits.readings"

// maxReadings bounds the persisted readings: a new window type is ignored
// once the book holds this many.
const maxReadings = 16

// Reading is the latest observed usage of one subscription window.
// Book.Gate (policy.go) gates agent jobs on it (#63).
type Reading struct {
	Window      string    `json:"window"`
	Utilization float64   `json:"utilization"` // as the stream reports it
	Status      string    `json:"status"`
	ResetsAt    time.Time `json:"resets_at"`
	ObservedAt  time.Time `json:"observed_at"`
}

// loadReadings reads the persisted readings; called by Open.
func (b *Book) loadReadings(ctx context.Context) error {
	v, err := b.meta.Meta(ctx, readingsKey)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("open limits readings: %w", err)
	}
	var got map[string]Reading
	if err := json.Unmarshal([]byte(v), &got); err != nil {
		b.log.Warn("limits.corrupt", "key", readingsKey, "error", err)
		return nil
	}
	if got != nil {
		b.readings = got
	}
	return nil
}

// Observe records the latest reading of each window in ws, stamped with the
// book's clock, and persists them. A window whose reading is unchanged is
// skipped, so observing the same step twice neither logs nor writes twice.
// A new window type is ignored once the book holds 16. An empty ws is a no-op. On a persist error memory is unchanged.
func (b *Book) Observe(ctx context.Context, ws []agent.Window) error {
	if len(ws) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	next := make(map[string]Reading, len(b.readings)+len(ws))
	for k, r := range b.readings {
		next[k] = r
	}
	var changed []Reading
	for _, w := range ws {
		r := Reading{Window: w.Type, Utilization: w.Utilization, Status: w.Status, ResetsAt: w.ResetsAt.UTC(), ObservedAt: now}
		old, ok := next[w.Type]
		if !ok && len(next) >= maxReadings {
			continue
		}
		if ok && old.Utilization == r.Utilization && old.Status == r.Status && old.ResetsAt.Equal(r.ResetsAt) {
			continue
		}
		next[w.Type] = r
		changed = append(changed, r)
	}
	if len(changed) == 0 {
		return nil
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode limits readings: %w", err)
	}
	if err := b.meta.SetMeta(ctx, readingsKey, string(raw)); err != nil {
		return fmt.Errorf("persist limits readings: %w", err)
	}
	b.readings = next
	for _, r := range changed {
		b.log.Info("limits.reading", "window", r.Window, "utilization", r.Utilization, "status", r.Status, "resets_at", r.ResetsAt)
	}
	return nil
}

// Readings returns a copy of the latest reading per window, sorted by window.
func (b *Book) Readings() []Reading {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Reading, 0, len(b.readings))
	for _, r := range b.readings {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Window < out[j].Window })
	return out
}
