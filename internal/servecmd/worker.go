package servecmd

import (
	"context"
	"os"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/worker"
)

// CommitCode implements worker.CodeCommitter on the shared per-branch
// committer.
func (l *lazyBranches) CommitCode(ctx context.Context, branch string, req committer.CodeRequest) (committer.Result, error) {
	b, err := l.get(ctx)
	if err != nil {
		return committer.Result{}, err
	}
	return b.CommitCode(ctx, branch, req)
}

// workerLoop builds the worker loop (#33). It reuses the process-wide
// executor, Finisher, result parser and branch committer, so it adds no second
// Closer, committer or executor. It returns nil, and logs, when the executor
// cannot be built.
func workerLoop(w *wiring) controller.Loop {
	ex, err := w.newExecutor()
	if err != nil {
		w.Log.Error("worker.unavailable", "error", err)
		return nil
	}
	fin, err := w.finisher()
	if err != nil {
		w.Log.Error("worker.unavailable", "error", err)
		return nil
	}
	reg, err := agent.NewRegistry(claude.New(claude.Options{}))
	if err != nil {
		panic(err) // a duplicate adapter name is a programming error
	}
	return worker.New(worker.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store,
		Executor: ex, Finisher: fin, Code: w.branchCommitter(), Git: gitx.New(""),
		Pool:   func() worker.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model, Readings: readingObserver{w},
		Secrets: func() *redact.Secrets { return redact.FromEnviron(os.Environ()) }, HostPaths: w.hostPaths(),
		Repo: w.Repo, RepoDir: w.Root, RemoteURL: w.RemoteURL, Token: w.Token, Log: w.Log,
	})
}
