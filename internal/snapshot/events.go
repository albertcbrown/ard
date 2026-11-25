package snapshot

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Event is one recent log record.
type Event struct {
	Time  time.Time
	Level slog.Level
	Msg   string
	Attrs string // flattened "k=v k=v", groups as "g.k=v"
}

// Ring is a fixed-size, concurrency-safe buffer of the newest events.
type Ring struct {
	mu   sync.Mutex
	buf  []Event
	next int
	full bool
}

// NewRing returns a ring holding the newest n events (n < 1 is treated as 1).
func NewRing(n int) *Ring { return &Ring{buf: make([]Event, max(1, n))} }

// Append adds e, dropping the oldest event when the ring is full.
func (r *Ring) Append(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// Events returns a copy of the buffered events, oldest first.
func (r *Ring) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]Event(nil), r.buf[:r.next]...)
	}
	out := make([]Event, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	return append(out, r.buf[:r.next]...)
}

type tee struct {
	next   slog.Handler
	ring   *Ring
	redact func(string) string
	attrs  string // pre-flattened WithAttrs, already prefixed by groups
	groups []string
}

// NewTee returns a handler that passes every record to next unchanged and
// also records it in ring. redact, when non-nil, is applied to the recorded
// text only; next's output is never altered.
func NewTee(next slog.Handler, ring *Ring, redact func(string) string) slog.Handler {
	return &tee{next: next, ring: ring, redact: redact}
}

func (t *tee) Enabled(ctx context.Context, l slog.Level) bool { return t.next.Enabled(ctx, l) }

func (t *tee) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(t.attrs)
	prefix := strings.Join(t.groups, ".")
	r.Attrs(func(a slog.Attr) bool {
		flatten(&b, prefix, a)
		return true
	})
	msg, attrs := r.Message, strings.TrimSpace(b.String())
	if t.redact != nil {
		msg, attrs = t.redact(msg), t.redact(attrs)
	}
	t.ring.Append(Event{Time: r.Time, Level: r.Level, Msg: msg, Attrs: attrs})
	return t.next.Handle(ctx, r)
}

func (t *tee) WithAttrs(as []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(t.attrs)
	prefix := strings.Join(t.groups, ".")
	for _, a := range as {
		flatten(&b, prefix, a)
	}
	c := *t
	c.next = t.next.WithAttrs(as)
	c.attrs = b.String()
	return &c
}

func (t *tee) WithGroup(name string) slog.Handler {
	c := *t
	c.next = t.next.WithGroup(name)
	if name != "" {
		c.groups = append(append([]string(nil), t.groups...), name)
	}
	return &c
}

func flatten(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if a.Value.Kind() == slog.KindGroup {
		if key != "" {
			if prefix != "" {
				prefix += "."
			}
			prefix += key
		}
		for _, g := range a.Value.Group() {
			flatten(b, prefix, g)
		}
		return
	}
	if prefix != "" {
		key = prefix + "." + key
	}
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(a.Value.String())
	b.WriteByte(' ')
}
