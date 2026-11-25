package ui

import (
	"context"
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

	snap          snapshot.Snapshot
	width, height int
	selected      int
	help          bool
	confirmQuit   bool
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
		return m, m.fetch()
	case snapMsg:
		m.snap = snapshot.Snapshot(msg)
		m.move(0)
		return m, m.tick()
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
