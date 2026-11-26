package servecmd

import (
	"context"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/repopath"
	"github.com/albertcbrown/ard/internal/scanner"
)

// scannerLoop builds the scanners loop (#23). Everything that touches git or
// the pool is created lazily: the pool does not exist until the pool-adopt
// startup hook has run, and a repo with no scanners never needs a committer.
func scannerLoop(w *wiring) controller.Loop {
	ex, err := w.newExecutor()
	if err != nil {
		w.Log.Error("scanner.executor_unavailable", "error", err)
		return nil
	}
	w.initMu.Lock()
	mode := w.mode
	w.initMu.Unlock()
	deps := scanner.ExecDeps{
		Pool:     func() scanner.Offerer { return w.Pool() },
		Executor: ex,
		Jobs:     w.Store,
		Repo:     w.Repo.String(), RepoDir: w.Root,
		Log: w.Log,
	}
	return scanner.NewLoop(scannerPipeline(w, w.mainCommitter(), scannerEngines(w, deps, mode)))
}

// scannerEngines routes scan executables to the exec engine and prompt.txt
// scanners to the agent engine (#204). With no model credential Agent stays
// nil (never a typed nil), so LLM scanners are skipped with
// reason=no_model_credential and never recorded; the spark pod runs this way.
// Under executor: k8s, which has no output dir for a read-only job yet (#454),
// they are skipped with reason=executor_unsupported.
func scannerEngines(w *wiring, deps scanner.ExecDeps, mode string) scanner.Engines {
	e := scanner.Engines{Exec: scanner.NewExecEngine(deps)}
	switch {
	case w.Model.IsZero():
	case mode == "k8s":
		e.Agent = scanner.Unavailable{Err: scanner.ErrExecutorUnsupported}
	default:
		reg, err := agent.NewRegistry(claude.New(claude.Options{}))
		if err != nil {
			panic(err) // a duplicate adapter name is a programming error
		}
		e.Agent = scanner.NewAgentEngine(scanner.AgentDeps{
			ExecDeps: deps, Agents: reg, Cred: w.Model, Usage: w.Store,
			Limits: func() scanner.LimitRecorder {
				b, err := w.limitsBook(context.Background())
				if err != nil || b == nil {
					// The engine then fails a limited run closed (recorded).
					w.Log.Error("scanner.limits_unavailable", "error", err)
					return nil
				}
				return b
			},
		})
	}
	return e
}

// scannerPipeline builds the pipeline the scanners loop runs.
func scannerPipeline(w *wiring, lc scanner.Committer, eng scanner.Engine) *scanner.Pipeline {
	return &scanner.Pipeline{
		Reader: w.Reader, Effects: w.Effects, Committer: lc, Engine: eng,
		Stores: scanner.RegistryStores(w.Scanners), Repo: w.Repo.String(), Log: w.Log, Paused: w.Paused, BodyLocks: &w.bodyLocks,
		StatusIssue: func() int { return int(w.statusIssue.Load()) },
		// The pre-filing guard's known values, Kubernetes job tokens included.
		Secrets: func() *redact.Secrets { return redact.FromEnviron(w.environ()) },
	}
}

// repoWorkDir is ~/.ard/work/<owner>/<repo>: the local executor's job
// worktrees, and where committer lanes lived before #113 (lanes now live under
// laneWorkDir; openClone deletes the old ones).
func repoWorkDir(home string, r github.Repo) (string, error) {
	return repopath.Join(filepath.Join(home, ".ard", "work"), r.String())
}
