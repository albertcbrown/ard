package limits

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
)

func TestObserveStoresSortedCopies(t *testing.T) {
	c := &clock{t0}
	b, buf := open(t, &fakeMeta{}, c)
	err := b.Observe(context.Background(), []agent.Window{
		{Type: "seven_day", Utilization: 0.4, Status: "allowed", ResetsAt: t0.Add(24 * time.Hour)},
		{Type: "five_hour", Utilization: 0.1, Status: "allowed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := b.Readings()
	if len(got) != 2 || got[0].Window != "five_hour" || got[1].Window != "seven_day" || !got[0].ObservedAt.Equal(t0) {
		t.Fatalf("readings = %+v", got)
	}
	got[0].Window = "mutated"
	if b.Readings()[0].Window != "five_hour" {
		t.Error("Readings returned shared state")
	}
	if n := strings.Count(buf.String(), "limits.reading"); n != 2 {
		t.Errorf("limits.reading logged %d times:\n%s", n, buf)
	}
}

func TestObserveOverwritesAndReopens(t *testing.T) {
	m := &fakeMeta{}
	c := &clock{t0}
	b, _ := open(t, m, c)
	ctx := context.Background()
	_ = b.Observe(ctx, []agent.Window{{Type: "five_hour", Utilization: 0.1}})
	c.advance(time.Minute)
	_ = b.Observe(ctx, []agent.Window{{Type: "five_hour", Utilization: 0.7, Status: "allowed_warning"}})
	b2, _ := open(t, m, c)
	got := b2.Readings()
	if len(got) != 1 || got[0].Utilization != 0.7 || got[0].Status != "allowed_warning" || !got[0].ObservedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("reopened = %+v", got)
	}
}

func TestObserveUnchangedAndEmptyAreNoOps(t *testing.T) {
	m := &fakeMeta{}
	b, _ := open(t, m, &clock{t0})
	ctx := context.Background()
	if err := b.Observe(ctx, nil); err != nil || m.setCalls != 0 {
		t.Fatalf("empty: err=%v sets=%d", err, m.setCalls)
	}
	ws := []agent.Window{{Type: "five_hour", Utilization: 0.1}}
	_ = b.Observe(ctx, ws)
	_ = b.Observe(ctx, ws)
	if m.setCalls != 1 {
		t.Errorf("setCalls = %d, want 1", m.setCalls)
	}
}

func TestObservePersistErrorKeepsMemory(t *testing.T) {
	m := &fakeMeta{setErr: errors.New("disk")}
	b, _ := open(t, m, &clock{t0})
	if err := b.Observe(context.Background(), []agent.Window{{Type: "five_hour", Utilization: 0.1}}); err == nil {
		t.Fatal("want error")
	}
	if len(b.Readings()) != 0 {
		t.Error("memory changed on persist error")
	}
}

func TestReadingsCorruptValue(t *testing.T) {
	m := &fakeMeta{m: map[string]string{readingsKey: "{not json"}}
	b, buf := open(t, m, &clock{t0})
	if len(b.Readings()) != 0 || !strings.Contains(buf.String(), "limits.corrupt") {
		t.Errorf("readings=%v log=%s", b.Readings(), buf)
	}
}

func TestExhaustAndReadingsIndependent(t *testing.T) {
	m := &fakeMeta{}
	c := &clock{t0}
	b, _ := open(t, m, c)
	ctx := context.Background()
	_ = b.Observe(ctx, []agent.Window{{Type: "five_hour", Utilization: 0.1}})
	if _, err := b.Exhaust(ctx, "", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	b2, _ := open(t, m, c)
	if len(b2.Readings()) != 1 || b2.Until("").IsZero() {
		t.Errorf("readings=%v until=%v", b2.Readings(), b2.Until(""))
	}
}
