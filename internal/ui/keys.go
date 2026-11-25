package ui

import (
	"fmt"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"
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
	if it := m.confirmApprove; it != nil {
		m.confirmApprove = nil
		if key != "y" || m.approve == nil {
			return m, nil // default No: every other key cancels
		}
		n, run := it.Issue, m.approve
		m.status = fmt.Sprintf("approving #%d...", n)
		return m, func() tea.Msg {
			line, err := run(n)
			return approveDoneMsg{n: n, line: line, err: err}
		}
	}
	switch key {
	case "a":
		if m.approve != nil && m.selected >= 0 && m.selected < len(m.snap.Issues) {
			n := m.snap.Issues[m.selected].Issue
			for _, q := range m.snap.Queue {
				if q.Issue == n && n > 0 {
					q := q
					m.confirmApprove = &q
					m.status = ""
					break
				}
			}
		}
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
