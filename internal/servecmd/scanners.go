package servecmd

import (
	"path/filepath"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
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
	lc := w.mainCommitter()
	eng := scanner.NewExecEngine(scanner.ExecDeps{
		Pool:     func() scanner.Offerer { return w.Pool() },
		Executor: ex,
		Jobs:     w.Store,
		Repo:     w.Repo.String(), RepoDir: w.Root,
		Log: w.Log,
	})
	return scanner.NewLoop(&scanner.Pipeline{
		Reader: w.Reader, Effects: w.Effects, Committer: lc, Engine: eng,
		Stores: scanner.RegistryStores(w.Scanners), Repo: w.Repo.String(), Log: w.Log, Paused: w.Paused,
	})
}

// repoWorkDir is ~/.ard/work/<owner>/<repo>: the local executor's job
// worktrees, and where committer lanes lived before #113 (lanes now live under
// laneWorkDir; openClone deletes the old ones).
func repoWorkDir(home string, r github.Repo) (string, error) {
	return repopath.Join(filepath.Join(home, ".ard", "work"), r.String())
}
