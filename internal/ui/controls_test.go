package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/snapshot"
)

type fakeCtl struct {
	pause, unpause, up, down int
	err                      error
}

func (f *fakeCtl) Pause() error              { f.pause++; return f.err }
func (f *fakeCtl) Unpause() error            { f.unpause++; return f.err }
func (f *fakeCtl) SlotUp() (string, error)   { f.up++; return "max_pods 4 (config 3)", f.err }
func (f *fakeCtl) SlotDown() (string, error) { f.down++; return "max_pods 2", f.err }

func pressKey(m Model, s string) Model {
	r := []rune(s)
	var k tea.KeyPressMsg
	if s == "enter" {
		k = tea.KeyPressMsg{Code: tea.KeyEnter}
	} else {
		k = tea.KeyPressMsg{Code: r[0], Text: s}
	}
	nm, _ := m.handleKey(k)
	return nm
}

func ctlModel(f *fakeCtl, pause ...snapshot.PauseView) (Model, *time.Time) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	m := model(100, 30, snapshot.Snapshot{TakenAt: now, Pause: pause}).WithControls(f)
	m.now = func() time.Time { return now }
	return m, &now
}

func TestPauseKeyWritesAtOnce(t *testing.T) {
	f := &fakeCtl{}
	m, _ := ctlModel(f)
	m = pressKey(m, "p")
	if f.pause != 1 || m.confirmUnpause {
		t.Fatalf("pause=%d confirm=%v", f.pause, m.confirmUnpause)
	}
	f.err = errors.New("disk full")
	m = pressKey(m, "p")
	if !strings.Contains(m.toast, "pause failed") {
		t.Errorf("toast %q", m.toast)
	}
}

func TestUnpauseAsksToConfirm(t *testing.T) {
	f := &fakeCtl{}
	file := snapshot.PauseView{Loop: "w", Reason: controller.ReasonPauseFile}
	m, _ := ctlModel(f, file)
	m = pressKey(m, "p")
	if !m.confirmUnpause || f.pause != 0 || !strings.Contains(Render(m), "unpause?") || !strings.Contains(Render(m), "PAUSED (file)") {
		t.Fatalf("confirm=%v pause=%d", m.confirmUnpause, f.pause)
	}
	m = pressKey(m, "n")
	if f.unpause != 0 || m.confirmUnpause {
		t.Fatal("n must cancel")
	}
	m = pressKey(pressKey(m, "p"), "y")
	if f.unpause != 1 {
		t.Fatalf("unpause=%d", f.unpause)
	}
}

func TestPauseKeyLeavesOtherSources(t *testing.T) {
	for r, want := range map[controller.Reason]string{controller.ReasonLabel: "label", controller.ReasonConfig: "paused: false on main"} {
		f := &fakeCtl{}
		m, _ := ctlModel(f, snapshot.PauseView{Loop: "w", Reason: r})
		m = pressKey(m, "p")
		if f.pause+f.unpause != 0 || m.confirmUnpause || !strings.Contains(m.toast, want) {
			t.Errorf("%s: pause=%d unpause=%d toast=%q", r, f.pause, f.unpause, m.toast)
		}
	}
}

func TestSlotKeysToastAndExpiry(t *testing.T) {
	f := &fakeCtl{}
	m, now := ctlModel(f)
	m = pressKey(m, "+")
	if f.up != 1 || !strings.Contains(Render(m), "max_pods 4 (config 3)") {
		t.Fatalf("up=%d", f.up)
	}
	m = pressKey(m, "-")
	if f.down != 1 || m.toast != "max_pods 2" {
		t.Fatalf("down=%d toast=%q", f.down, m.toast)
	}
	*now = now.Add(ToastFor)
	nm, _ := m.Update(tickMsg{})
	if nm.(Model).toast != "" {
		t.Error("toast should expire")
	}
}

func TestNilControlsInert(t *testing.T) {
	m := model(100, 30, snapshot.Snapshot{})
	for _, k := range []string{"p", "+", "-"} {
		m = pressKey(m, k)
	}
	if m.toast != "" || m.confirmUnpause {
		t.Error("keys must be inert without controls")
	}
}

func TestHeaderOverrideDrainingAndHelp(t *testing.T) {
	s := snapshot.Snapshot{Pool: snapshot.PoolView{Used: 3, Max: 2, ConfigMax: 3, Override: true}}
	m := model(120, 40, s)
	m.help = true
	out := Render(m)
	for _, want := range []string{"max_pods 2 (config 3)", "draining", "dry-run: the pause file and slot override still apply locally"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}
