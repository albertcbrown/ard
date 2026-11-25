package ui

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/snapshot"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestClean(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m": "red",
		"\x1b]0;pwn\x07x":    "x",
		"a\u009bb":           "ab",
		"a\rb\nc\td":         "a b c d",
		"a\x00b\x7fc":        "abc",
		"a\u202eb":           "ab",
		"a\xffb":             "ab",
		"héllo 🎉 日本":         "héllo 🎉 日本",
		"\x1bPq\x1b\\after":  "after",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Trunc("日本語日本語", 5); ansi.StringWidth(got) > 5 {
		t.Errorf("Trunc too wide: %q", got)
	}
}

func fixed() snapshot.Snapshot {
	f := 1.25
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := snapshot.Snapshot{
		TakenAt: t0,
		Pool:    snapshot.PoolView{Used: 2, Max: 4, Waiting: []snapshot.Waiting{{Class: "worker", Role: "engineer", Reason: snapshot.ReasonSlot, Count: 3, Oldest: 90 * time.Second}}},
		Running: []snapshot.RunningJob{{JobID: "j1", Issue: 414, Stage: "worker", Role: "engineer", Elapsed: 5 * time.Minute, Timeout: time.Hour, FinishedCostUSD: &f}},
		Pause:   []snapshot.PauseView{{Loop: "workers", Reason: controller.ReasonLabel}},
		Ticks:   []snapshot.TickView{{Loop: "triage", Count: 7, End: t0}},
		Limits: snapshot.LimitsView{
			Windows: []snapshot.WindowView{{Window: "5h", Utilization: 0.42, Status: "allowed", ResetsAt: t0.Add(time.Hour)}},
			Blocks:  []snapshot.BlockView{{Family: "opus", Until: t0.Add(2 * time.Hour)}},
		},
		Spend:  snapshot.SpendView{RepoUSD: 3.5, RepoCap: 20},
		Events: []snapshot.Event{{Time: t0, Level: slog.LevelInfo, Msg: "serve.start", Attrs: "repo=o/r"}},
		Errors: []string{"state: boom"},
	}
	for i := 1; i <= 60; i++ {
		s.Issues = append(s.Issues, snapshot.IssueView{Issue: i, State: "triage:engineer done", Jobs: 1, CostUSD: &f})
	}
	return s
}

func model(w, h int, s snapshot.Snapshot) Model {
	m := New("o/r", "/home/u/.ard/logs/o/r/serve.log", nil, nil)
	m.width, m.height, m.snap = w, h, s
	return m
}

func TestGolden(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		s    snapshot.Snapshot
	}{
		{"80x24", 80, 24, fixed()},
		{"160x50", 160, 50, fixed()},
		{"empty", 80, 24, snapshot.Snapshot{}},
		{"tiny", 10, 4, fixed()},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ansi.Strip(Render(model(c.w, c.h, c.s))) + "\n"
			if lines := strings.Count(got, "\n"); lines > c.h && c.name != "tiny" {
				t.Errorf("%d lines > height %d", lines, c.h)
			}
			for _, l := range strings.Split(got, "\n") {
				if ansi.StringWidth(l) > c.w && c.name != "tiny" {
					t.Errorf("line too wide: %q", l)
				}
			}
			p := filepath.Join("testdata", c.name+".golden")
			if *update {
				if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("frame differs from %s:\n%s", p, got)
			}
		})
	}
	big := ansi.Strip(Render(model(160, 50, fixed())))
	small := ansi.Strip(Render(model(80, 24, fixed())))
	if !strings.Contains(big, "Recent events") || strings.Contains(small, "Recent events") {
		t.Error("events panel must appear only on the large frame")
	}
}

func TestEscapesInert(t *testing.T) {
	s := fixed()
	evil := "\x1b]0;pwn\x07\x1b[2J\x9b31m"
	s.Errors = []string{evil + "err"}
	s.Running[0].Role = evil + "role"
	s.Pause[0].Err = evil
	s.Events = []snapshot.Event{{Msg: evil + "msg", Attrs: evil}}
	s.Issues[0].State = evil + "state"
	for _, sz := range [][2]int{{80, 24}, {160, 50}} {
		out := Render(model(sz[0], sz[1], s))
		// Only SGR style sequences may remain.
		stripped := ansi.Strip(out)
		if strings.ContainsAny(stripped, "\x1b\x07\x9b") {
			t.Fatalf("control characters survived: %q", stripped)
		}
		if strings.Contains(out, "]0;pwn") || strings.Contains(out, "[2J") {
			t.Fatal("escape payload survived")
		}
	}
}

func key(s string) tea.Msg {
	switch s {
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func send(m Model, msgs ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range msgs {
		var nm tea.Model
		nm, cmd = m.Update(key(k))
		m = nm.(Model)
	}
	return m, cmd
}

func isQuit(c tea.Cmd) bool {
	if c == nil {
		return false
	}
	_, ok := c().(tea.QuitMsg)
	return ok
}

func TestKeys(t *testing.T) {
	var opened []string
	m := model(80, 24, fixed())
	m.open = func(u string) error { opened = append(opened, u); return nil }

	m, _ = send(m, "k", "up")
	if m.selected != 0 {
		t.Errorf("selection below 0: %d", m.selected)
	}
	m, _ = send(m, "j", "down", "j")
	if m.selected != 3 {
		t.Errorf("selected = %d", m.selected)
	}
	m, _ = send(m, "o")
	if len(opened) != 1 || opened[0] != "https://github.com/o/r/issues/4" {
		t.Errorf("opened %v", opened)
	}
	for i := 0; i < 100; i++ {
		m, _ = send(m, "j")
	}
	if m.selected != 59 {
		t.Errorf("selection not clamped: %d", m.selected)
	}
	m, _ = send(m, "?")
	if !m.help {
		t.Error("? should show help")
	}
	m, _ = send(m, "?")
	if m.help {
		t.Error("? should hide help")
	}
	m, c := send(m, "q")
	if !m.confirmQuit || isQuit(c) {
		t.Error("q must ask first")
	}
	m, c = send(m, "n")
	if m.confirmQuit || isQuit(c) {
		t.Error("n must cancel")
	}
	m, c = send(m, "q", "y")
	if !isQuit(c) {
		t.Error("q y must quit")
	}
	if _, c = send(model(80, 24, fixed()), "ctrl+c"); !isQuit(c) {
		t.Error("ctrl+c must quit at once")
	}
	empty := model(80, 24, snapshot.Snapshot{})
	empty.open = func(string) error { t.Error("open on empty list"); return nil }
	send(empty, "o", "j")
}

func TestUpdateTickAndResize(t *testing.T) {
	calls := 0
	m := New("o/r", "", func(context.Context) snapshot.Snapshot { calls++; return fixed() }, nil)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if m.width != 120 || m.height != 30 {
		t.Fatalf("size %dx%d", m.width, m.height)
	}
	msg := m.Init()()
	if calls != 1 {
		t.Fatalf("take called %d times", calls)
	}
	nm, cmd := m.Update(msg)
	m = nm.(Model)
	if len(m.snap.Issues) != 60 || cmd == nil {
		t.Fatal("snapshot not stored or tick not scheduled")
	}
	nm, cmd = m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("tick must fetch")
	}
	cmd()
	if calls != 2 {
		t.Errorf("take called %d times", calls)
	}
	_ = fmt.Sprint(nm)
}
