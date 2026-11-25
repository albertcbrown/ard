package ui

import (
	"fmt"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/albertcbrown/ard/internal/controller"
)

// IssueURL is the browser URL of issue n; it is built from the integer only.
func IssueURL(repo string, n int) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, n)
}

// OpenBrowser opens url with the platform opener, no shell, errors ignored.
func OpenBrowser(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (m Model) handleKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.confirmQuit {
		if key == "y" || key == "enter" {
			return m, tea.Quit
		}
		m.confirmQuit = false
		return m, nil
	}
	if m.confirmUnpause {
		m.confirmUnpause = false
		if key == "y" || key == "enter" {
			if err := m.ctl.Unpause(); err != nil {
				m.setToast("unpause failed: " + err.Error())
			} else {
				m.setToast("unpaused")
			}
		}
		return m, nil
	}
	switch key {
	case "p":
		m.pauseKey()
	case "+", "=":
		m.slotKey(func() (string, error) { return m.ctl.SlotUp() })
	case "-", "_":
		m.slotKey(func() (string, error) { return m.ctl.SlotDown() })
	case "?":
		m.help = !m.help
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "o":
		if m.selected >= 0 && m.selected < len(m.snap.Issues) && m.open != nil {
			if n := m.snap.Issues[m.selected].Issue; n > 0 {
				_ = m.open(IssueURL(m.Repo, n))
			}
		}
	case "q":
		m.confirmQuit = true
	}
	return m, nil
}

func (m *Model) move(d int) {
	n := len(m.snap.Issues)
	if n == 0 {
		m.selected = 0
		return
	}
	m.selected += d
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected > n-1 {
		m.selected = n - 1
	}
}

// liftHint says how to lift a pause the UI does not own.
func liftHint(r controller.Reason) string {
	switch r {
	case controller.ReasonLabel:
		return "paused by the ard:pause label: remove it from the status issue"
	case controller.ReasonConfig:
		return "paused by config: set paused: false on main"
	case controller.ReasonLocalConfig:
		return "paused by local config: set paused: false in the working tree"
	default:
		return "paused by " + string(r) + ": fix the cause, p does not lift it"
	}
}

func (m *Model) pauseKey() {
	if m.ctl == nil {
		return
	}
	var other controller.Reason
	for _, p := range m.snap.Pause {
		switch p.Reason {
		case controller.ReasonPauseFile:
			m.confirmUnpause = true
			return
		case controller.ReasonPauseFileUnreadable:
			m.setToast("pause file path is unreadable: fix it by hand")
			return
		default:
			other = p.Reason
		}
	}
	if other != "" {
		m.setToast(liftHint(other))
		return
	}
	if err := m.ctl.Pause(); err != nil {
		m.setToast("pause failed: " + err.Error())
		return
	}
	m.setToast("paused (pause file written)")
}

func (m *Model) slotKey(f func() (string, error)) {
	if m.ctl == nil {
		return
	}
	msg, err := f()
	if err != nil {
		msg = "slots: " + err.Error()
	}
	m.setToast(msg)
}
