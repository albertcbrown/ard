package servecmd

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSlots() (*slotOverride, *bytes.Buffer) {
	var buf bytes.Buffer
	return &slotOverride{log: slog.New(slog.NewTextHandler(&buf, nil))}, &buf
}

func TestOverrideClampsToBounds(t *testing.T) {
	for _, c := range []struct{ cfg, want int }{{3, 6}, {10, 16}, {1, 2}} {
		s, _ := newSlots()
		if got := s.Set(100, c.cfg); got != c.want {
			t.Errorf("cfg %d: max %d, want %d", c.cfg, got, c.want)
		}
		if _, ch := s.Step(1, c.cfg); ch {
			t.Errorf("cfg %d: step at upper bound changed", c.cfg)
		}
	}
	s, _ := newSlots()
	if got := s.Set(-5, 3); got != 1 {
		t.Errorf("min: %d", got)
	}
	if _, ch := s.Step(-1, 3); ch {
		t.Error("step at 1 changed")
	}
	if n, ch := s.Step(1, 3); n != 2 || !ch {
		t.Errorf("step up: %d %v", n, ch)
	}
}

func TestOverrideSurvivesReloadUntilConfigChanges(t *testing.T) {
	s, buf := newSlots()
	s.Set(5, 3)
	for i := 0; i < 3; i++ {
		if got := s.Effective(3); got != 5 {
			t.Fatalf("reload %d: %d", i, got)
		}
	}
	if n, on := s.Active(3); n != 5 || !on {
		t.Fatalf("active %d %v", n, on)
	}
	if got := s.Effective(4); got != 4 {
		t.Fatalf("changed config: %d", got)
	}
	if !strings.Contains(buf.String(), "reason=config_changed") || !strings.Contains(buf.String(), "pool.max_override") {
		t.Errorf("log: %s", buf.String())
	}
	if got := s.Effective(4); got != 4 {
		t.Errorf("after clear: %d", got)
	}
	if _, on := s.Active(4); on {
		t.Error("override still active")
	}
}

func TestOverrideNeverTouchesConfigFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("max_pods: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	s, _ := newSlots()
	s.Set(5, 3)
	s.Effective(3)
	s.Effective(4)
	after, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	if string(b) != "max_pods: 3\n" || !after.ModTime().Equal(before.ModTime()) {
		t.Error("config file changed")
	}
}

func TestWiringLogsOverride(t *testing.T) {
	var buf bytes.Buffer
	w := &wiring{Log: slog.New(slog.NewTextHandler(&buf, nil))}
	w.initSlots()
	w.slots.Set(5, 3)
	if got := w.reloadMax(3); got != 5 {
		t.Fatalf("reloadMax = %d, want 5", got)
	}
	if got := w.reloadMax(4); got != 4 {
		t.Fatalf("reloadMax after config change = %d, want 4", got)
	}
	out := buf.String()
	if !strings.Contains(out, "pool.max_override") || !strings.Contains(out, "config_changed") || !strings.Contains(out, "reason=key") {
		t.Errorf("log: %s", out)
	}
}
