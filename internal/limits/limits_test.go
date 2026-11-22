package limits

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/state"
)

type fakeMeta struct {
	m        map[string]string
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeMeta) Meta(_ context.Context, k string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.m[k]
	if !ok {
		return "", fmt.Errorf("meta %q: %w", k, state.ErrNotFound)
	}
	return v, nil
}

func (f *fakeMeta) SetMeta(_ context.Context, k, v string) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	if f.m == nil {
		f.m = map[string]string{}
	}
	f.m[k] = v
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

var t0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func open(t *testing.T, m *fakeMeta, c *clock) (*Book, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	b, err := Open(context.Background(), m, c.now, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return b, &buf
}

func TestExhaustUntil(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		family string
		resets func(now time.Time) time.Time
		want   time.Duration
	}{
		{"zero reset is the default pause", "", func(time.Time) time.Time { return time.Time{} }, 30 * time.Minute},
		{"past reset is the default pause", "opus", func(n time.Time) time.Time { return n.Add(-time.Hour) }, 30 * time.Minute},
		{"reset equal to now is the default pause", "", func(n time.Time) time.Time { return n }, 30 * time.Minute},
		{"future reset is used", "", func(n time.Time) time.Time { return n.Add(3 * time.Hour) }, 3 * time.Hour},
		{"far reset is clamped to 8 days", "", func(n time.Time) time.Time { return n.Add(30 * 24 * time.Hour) }, MaxPause},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &clock{t0}
			b, _ := open(t, &fakeMeta{}, c)
			got, err := b.Exhaust(ctx, tc.family, tc.resets(c.t))
			if err != nil {
				t.Fatal(err)
			}
			if want := t0.Add(tc.want); !got.Equal(want) {
				t.Errorf("until = %v, want %v", got, want)
			}
			if u := b.Until(tc.family); !u.Equal(got) {
				t.Errorf("Until = %v, want %v", u, got)
			}
		})
	}
}

func TestExhaustKeepsLaterAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	c := &clock{t0}
	m := &fakeMeta{}
	b, logs := open(t, m, c)
	late, _ := b.Exhaust(ctx, "", t0.Add(3*time.Hour))
	got, _ := b.Exhaust(ctx, "", t0.Add(time.Hour))
	if !got.Equal(late) {
		t.Errorf("an earlier time won: %v, want %v", got, late)
	}
	got, _ = b.Exhaust(ctx, "", t0.Add(5*time.Hour))
	if !got.Equal(t0.Add(5 * time.Hour)) {
		t.Errorf("a later time lost: %v", got)
	}
	again, _ := b.Exhaust(ctx, "", t0.Add(5*time.Hour))
	if !again.Equal(got) {
		t.Errorf("not idempotent: %v then %v", got, again)
	}
	if n := strings.Count(logs.String(), "limits.exhausted"); n != 2 {
		t.Errorf("limits.exhausted logged %d times, want 2 (only when the time changes):\n%s", n, logs)
	}
}

func TestBackoffDoublesToCapAndResets(t *testing.T) {
	ctx := context.Background()
	c := &clock{t0}
	b, logs := open(t, &fakeMeta{}, c)
	want := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour}
	for i, w := range want {
		start := c.t
		got, err := b.Exhaust(ctx, "", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Sub(start) != w {
			t.Fatalf("stop %d: pause %v, want %v", i+1, got.Sub(start), w)
		}
		// Another job that was already running hits the same limit: no step.
		again, _ := b.Exhaust(ctx, "", time.Time{})
		if !again.Equal(got) {
			t.Fatalf("stop %d: a second stop during the pause changed it to %v", i+1, again)
		}
		c.t = got // the pause runs out
	}
	if n := strings.Count(logs.String(), "limits.backoff"); n != len(want) {
		t.Errorf("limits.backoff logged %d times, want %d", n, len(want))
	}
	if err := b.Succeeded(ctx, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Exhaust(ctx, "", time.Time{})
	if got.Sub(c.t) != 30*time.Minute {
		t.Errorf("after a success the pause is %v, want 30m", got.Sub(c.t))
	}
}

func TestKnownResetEndsBackoff(t *testing.T) {
	ctx := context.Background()
	c := &clock{t0}
	b, _ := open(t, &fakeMeta{}, c)
	u, _ := b.Exhaust(ctx, "", time.Time{})
	c.t = u
	u, _ = b.Exhaust(ctx, "", time.Time{}) // step 2: 1h
	c.t = u
	u, _ = b.Exhaust(ctx, "", c.t.Add(time.Hour)) // a real reset
	c.t = u
	got, _ := b.Exhaust(ctx, "", time.Time{})
	if got.Sub(c.t) != 30*time.Minute {
		t.Errorf("pause after a known reset = %v, want 30m", got.Sub(c.t))
	}
}

func TestSucceededScope(t *testing.T) {
	ctx := context.Background()
	c := &clock{t0}
	b, _ := open(t, &fakeMeta{}, c)
	for _, f := range []string{"opus", "sonnet"} {
		u, _ := b.Exhaust(ctx, f, time.Time{})
		c.t = u
		u, _ = b.Exhaust(ctx, f, time.Time{})
		c.t = u
	}
	if err := b.Succeeded(ctx, "opus"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Exhaust(ctx, "opus", time.Time{}); got.Sub(c.t) != 30*time.Minute {
		t.Errorf("opus pause = %v, want 30m", got.Sub(c.t))
	}
	if got, _ := b.Exhaust(ctx, "sonnet", time.Time{}); got.Sub(c.t) != 2*time.Hour {
		t.Errorf("sonnet pause = %v, want 2h (not reset by an opus success)", got.Sub(c.t))
	}
}

func TestUntilFamilies(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		exhausted string
		ask       string
		blocked   bool
	}{
		{"account blocks opus", "", "opus", true},
		{"account blocks sonnet", "", "sonnet", true},
		{"account blocks unknown", "", "", true},
		{"opus blocks opus", "opus", "opus", true},
		{"opus does not block sonnet", "opus", "sonnet", false},
		{"opus does not block haiku", "opus", "haiku", false},
		{"opus blocks unknown (fails closed)", "opus", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &clock{t0}
			b, _ := open(t, &fakeMeta{}, c)
			until, _ := b.Exhaust(ctx, tc.exhausted, t0.Add(time.Hour))
			if got := !b.Until(tc.ask).IsZero(); got != tc.blocked {
				t.Errorf("blocked = %v, want %v", got, tc.blocked)
			}
			if tc.blocked {
				c.t = until.Add(-time.Second)
				if b.Until(tc.ask).IsZero() {
					t.Error("free one second early")
				}
				c.t = until
				if !b.Until(tc.ask).IsZero() {
					t.Error("still blocked at the reset time")
				}
			}
		})
	}
}

func TestPersistence(t *testing.T) {
	ctx := context.Background()
	c := &clock{t0}
	m := &fakeMeta{}
	b, _ := open(t, m, c)
	u, _ := b.Exhaust(ctx, "opus", t0.Add(2*time.Hour))
	b2, _ := open(t, m, c)
	if got := b2.Until("opus"); !got.Equal(u) {
		t.Errorf("after reopen Until = %v, want %v", got, u)
	}

	t.Run("malformed value gives an empty book and a warning", func(t *testing.T) {
		bad := &fakeMeta{m: map[string]string{metaKey: "{not json"}}
		b, logs := open(t, bad, c)
		if !b.Until("opus").IsZero() || !strings.Contains(logs.String(), "limits.corrupt") {
			t.Errorf("until = %v, logs = %s", b.Until("opus"), logs)
		}
		if _, err := b.Exhaust(ctx, "", t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(bad.m[metaKey], "not json") {
			t.Error("the corrupt value was not overwritten")
		}
	})
	t.Run("a read error is returned", func(t *testing.T) {
		if _, err := Open(ctx, &fakeMeta{getErr: errors.New("db down")}, c.now, nil); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("a write error is returned and memory is unchanged", func(t *testing.T) {
		m := &fakeMeta{setErr: errors.New("disk full")}
		b, _ := open(t, m, c)
		if _, err := b.Exhaust(ctx, "", t0.Add(time.Hour)); err == nil {
			t.Fatal("want an error")
		}
		if !b.Until("").IsZero() {
			t.Error("the book changed although it could not be persisted")
		}
	})
}

func TestFamilyOf(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-4-1": "opus", "opus": "opus", "Sonnet": "sonnet", "claude-haiku-4-5": "haiku", "": "", "kimi-k2": "",
	} {
		if got := FamilyOf(in); got != want {
			t.Errorf("FamilyOf(%q) = %q, want %q", in, got, want)
		}
	}
}
