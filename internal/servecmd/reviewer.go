package servecmd

import (
	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/reviewer"
)

// reviewerLoop builds the reviewer loop (#35). It reuses the process-wide
// executor, Finisher and result parser, so it adds no second Closer,
// committer or executor. It returns nil, and logs, when the executor cannot
// be built.
func reviewerLoop(w *wiring) controller.Loop {
	ex, err := w.newExecutor()
	if err != nil {
		w.Log.Error("review.unavailable", "error", err)
		return nil
	}
	fin, err := w.finisher()
	if err != nil {
		w.Log.Error("review.unavailable", "error", err)
		return nil
	}
	reg, err := agent.NewRegistry(claude.New(claude.Options{}))
	if err != nil {
		panic(err) // a duplicate adapter name is a programming error
	}
	return reviewer.New(reviewer.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store,
		Executor: ex, Finisher: fin, Git: gitx.New(""),
		Pool:   func() reviewer.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo, RepoDir: w.Root, RemoteURL: w.RemoteURL, Token: w.Token, Log: w.Log,
	})
}
