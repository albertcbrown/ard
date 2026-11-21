package servecmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/scanner"
)

// scannerLoop builds the scanners loop (#23). Everything that touches git or
// the pool is created lazily: the pool does not exist until the pool-adopt
// startup hook has run, and a repo with no scanners never needs a committer.
func scannerLoop(w *wiring) controller.Loop {
	g := gitx.New("")
	ex, err := local.New(local.Options{Root: filepath.Join(w.Home, ".ard"), Log: w.Log})
	if err != nil {
		w.Log.Error("scanner.executor_unavailable", "error", err)
		return nil
	}
	remote := fmt.Sprintf("https://github.com/%s.git", w.Repo)
	lc := &lazyCommitter{w: w, g: g, remote: remote}
	w.onClose(lc.close)
	eng := scanner.NewExecEngine(scanner.ExecDeps{
		Git:      g,
		Pool:     func() scanner.Offerer { return w.Pool() },
		Executor: ex,
		Jobs:     w.Store,
		Repo:     w.Repo.String(), Root: w.Root, RepoDir: w.Root, RemoteURL: remote, Token: w.Token,
		Log: w.Log,
	})
	return scanner.NewLoop(&scanner.Pipeline{
		Reader: w.Reader, Effects: w.Effects, Committer: lc, Engine: eng,
		Stores: scanner.RegistryStores(w.Scanners), Repo: w.Repo.String(), Log: w.Log, Paused: w.Paused,
	})
}

// lazyCommitter builds the main-branch committer on first use. A failure to
// build it is returned to the caller and retried on the next commit.
type lazyCommitter struct {
	w      *wiring
	g      *gitx.Git
	remote string

	mu sync.Mutex
	c  *committer.Committer
}

func (l *lazyCommitter) get(ctx context.Context) (*committer.Committer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c != nil {
		return l.c, nil
	}
	id, err := l.w.Reader.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("identify github user: %w", err)
	}
	slug := strings.ToLower(l.w.Repo.Owner + "-" + l.w.Repo.Name)
	c, err := committer.NewMain(ctx, committer.Config{
		RepoDir: l.w.Root, WorkDir: filepath.Join(l.w.Home, ".ard", "work", slug),
		RemoteURL: l.remote, Bot: id, Token: l.w.Token, Log: l.w.Log,
	}, l.w.Effects, l.g)
	if err != nil {
		return nil, fmt.Errorf("start committer: %w", err)
	}
	l.c = c
	return c, nil
}

// Commit implements scanner.Committer.
func (l *lazyCommitter) Commit(ctx context.Context, req committer.Request) (committer.Result, error) {
	c, err := l.get(ctx)
	if err != nil {
		return committer.Result{}, err
	}
	return c.Commit(ctx, req)
}

func (l *lazyCommitter) close(ctx context.Context) {
	l.mu.Lock()
	c := l.c
	l.mu.Unlock()
	if c != nil {
		if err := c.Close(ctx); err != nil {
			l.w.Log.Warn("committer.close_failed", "error", err)
		}
	}
}
