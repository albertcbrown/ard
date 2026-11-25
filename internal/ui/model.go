package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/albertcbrown/ard/internal/snapshot"
)

// Model is the Bubble Tea model. It is read-only: it takes snapshots and
// draws them.
type Model struct {
	Repo    string // owner/repo
	LogPath string // shown in the header

	take func(context.Context) snapshot.Snapshot
	open func(url string) error
	// approve runs the approval and returns a status line. It may block.
	approve func(n int) (string, error)

	snap           snapshot.Snapshot
	width, height  int
	selected       int
	help           bool
	confirmQuit    bool
	confirmUnpause bool

	ctl            Controls
	now            func() time.Time
	toast          string
	toastUntil     time.Time
	confirmApprove *snapshot.QueueItem
	status         string // result of the last approval, shown in the footer
}

// Controls are the local actions the keys p, + and - perform. They are local
// controls (pause file, slot override), not GitHub effects.
type Controls interface {
	Pause() error
	Unpause() error
	SlotUp() (string, error)
	SlotDown() (string, error)
}

// ToastFor is how long a toast stays on screen.
const ToastFor = 4 * time.Second

// WithControls returns m with the p, + and - keys wired to c; nil keeps them inert.
func (m Model) WithControls(c Controls) Model { m.ctl = c; return m }

func (m Model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Model) setToast(s string) {
	m.toast, m.toastUntil = s, m.clock().Add(ToastFor)
}

type approveDoneMsg struct {
	n    int
	line string
	err  error
}

// WithApprove enables the approve key; fn runs the operator's approval.
func (m Model) WithApprove(fn func(n int) (string, error)) Model {
	m.approve = fn
	return m
}

type tickMsg struct{}
type snapMsg snapshot.Snapshot

// RefreshEvery is the redraw interval.
const RefreshEvery = time.Second

// New returns a Model. take must make no GitHub call; open may be nil.
func New(repo, logPath string, take func(context.Context) snapshot.Snapshot, open func(string) error) Model {
	return Model{Repo: repo, LogPath: logPath, take: take, open: open, width: 80, height: 24}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(RefreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) fetch() tea.Cmd {
	take := m.take
	return func() tea.Msg { return snapMsg(take(context.Background())) }
}

// Init takes the first snapshot at once.
func (m Model) Init() tea.Cmd { return m.fetch() }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		if m.toast != "" && !m.clock().Before(m.toastUntil) {
			m.toast = ""
		}
		return m, m.fetch()
	case snapMsg:
		m.snap = snapshot.Snapshot(msg)
		m.move(0)
		return m, m.tick()
	case approveDoneMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("approve #%d failed: %s", msg.n, Clean(msg.err.Error()))
		} else {
			m.status = Clean(msg.line)
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(Render(m))
	v.AltScreen = true
	return v
}
