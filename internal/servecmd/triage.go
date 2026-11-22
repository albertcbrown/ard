package servecmd

import (
	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/triage"
)

// triageLoop builds the triage loop (#29). It reuses the process-wide
// executor, Finisher and result parser, so it adds no second Closer,
// committer or executor. It returns nil, and logs, when the executor cannot
// be built.
func triageLoop(w *wiring) controller.Loop {
	ex, err := w.newExecutor()
	if err != nil {
		w.Log.Error("triage.unavailable", "error", err)
		return nil
	}
	fin, err := w.finisher()
	if err != nil {
		w.Log.Error("triage.unavailable", "error", err)
		return nil
	}
	reg, err := agent.NewRegistry(claude.New(claude.Options{}))
	if err != nil {
		panic(err) // a duplicate adapter name is a programming error
	}
	return triage.New(triage.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store,
		Executor: ex, Finisher: fin,
		Pool:   func() triage.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo.String(), RepoDir: w.Root, Log: w.Log,
	})
}
