package servecmd

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"github.com/albertcbrown/ard/internal/snapshot"
	"github.com/albertcbrown/ard/internal/ui"
)

// program is the part of a Bubble Tea program runWithUI needs; tests fake it.
type program interface {
	Run() (tea.Model, error)
	Send(tea.Msg)
}

// newProgram builds the real program on the terminal. A variable for tests.
var newProgram = func(m tea.Model, in io.Reader, out io.Writer) program {
	return tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out))
}

// runWithUI runs the controller and the read-only UI side by side. The UI
// ending (q, Ctrl-C) or ctx being canceled (SIGINT/SIGTERM) cancels the
// controller; runWithUI returns once run has stopped, so shutdown is the same
// graceful path as without a UI.
func runWithUI(ctx context.Context, run func(context.Context) error, take func(context.Context) snapshot.Snapshot, ctl ui.Controls,
	repo, logPath string, in io.Reader, out io.Writer) error {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(rctx) }()

	p := newProgram(ui.New(repo, logPath, take, ui.OpenBrowser).WithControls(ctl), in, out)
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.Send(tea.Quit())
		case err := <-done:
			// The controller ended by itself; put the result back and close the UI.
			done <- err
			p.Send(tea.Quit())
		case <-stopped:
		}
	}()
	_, uiErr := p.Run()
	close(stopped)
	cancel()
	err := <-done
	if err == nil && uiErr != nil && ctx.Err() == nil {
		err = fmt.Errorf("ard serve: ui: %w", uiErr)
	}
	return err
}
