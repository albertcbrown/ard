package servecmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/gitx"
)

// committerConfig is the identity, work directory and remote every committer
// of this process shares. A failure is returned to the caller and retried on
// the next commit.
func (w *wiring) committerConfig(ctx context.Context) (committer.Config, error) {
	id, err := w.Reader.Identity(ctx)
	if err != nil {
		return committer.Config{}, fmt.Errorf("identify github user: %w", err)
	}
	workDir, err := repoWorkDir(w.Home, w.Repo)
	if err != nil {
		return committer.Config{}, fmt.Errorf("start committer: %w", err)
	}
	return committer.Config{
		RepoDir: w.Root, WorkDir: workDir,
		RemoteURL: w.RemoteURL, Bot: id, Token: w.Token, Log: w.Log,
	}, nil
}

// mainCommitter returns the one main-branch committer of this process, shared
// by the scanners and close-out: two committers on one work directory would
// race. It is built on first use.
func (w *wiring) mainCommitter() *lazyCommitter {
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.mc == nil {
		w.mc = &lazyCommitter{w: w, g: gitx.New("")}
		w.onClose(w.mc.close)
	}
	return w.mc
}

// branchCommitter returns the shared per-branch committer (worker and review
// close-outs commit to PR branches). It is built on first use.
func (w *wiring) branchCommitter() *lazyBranches {
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.br == nil {
		w.br = &lazyBranches{w: w, g: gitx.New("")}
		w.onClose(w.br.close)
	}
	return w.br
}

// lazyCommitter builds the main-branch committer on first use. A failure to
// build it is returned to the caller and retried on the next commit.
type lazyCommitter struct {
	w *wiring
	g *gitx.Git

	mu sync.Mutex
	c  *committer.Committer
}

func (l *lazyCommitter) get(ctx context.Context) (*committer.Committer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c != nil {
		return l.c, nil
	}
	cfg, err := l.w.committerConfig(ctx)
	if err != nil {
		return nil, err
	}
	c, err := committer.NewMain(ctx, cfg, l.w.Effects, l.g)
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

// lazyBranches builds the per-branch committer on first use.
type lazyBranches struct {
	w *wiring
	g *gitx.Git

	mu sync.Mutex
	b  *committer.Branches
}

func (l *lazyBranches) get(ctx context.Context) (*committer.Branches, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b != nil {
		return l.b, nil
	}
	cfg, err := l.w.committerConfig(ctx)
	if err != nil {
		return nil, err
	}
	l.b = committer.NewBranches(cfg, l.w.Effects, l.g)
	return l.b, nil
}

func (l *lazyBranches) close(ctx context.Context) {
	l.mu.Lock()
	b := l.b
	l.mu.Unlock()
	if b != nil {
		if err := b.Close(ctx); err != nil {
			l.w.Log.Warn("committer.close_failed", "error", err)
		}
	}
}

// closeoutCommitter routes a close-out commit: branch "" goes to main, anything
// else to its PR branch. It implements closeout.Committer.
type closeoutCommitter struct{ w *wiring }

func (c closeoutCommitter) Commit(ctx context.Context, branch string, req committer.Request) (committer.Result, error) {
	if branch == "" {
		return c.w.mainCommitter().Commit(ctx, req)
	}
	b, err := c.w.branchCommitter().get(ctx)
	if err != nil {
		return committer.Result{}, err
	}
	return b.Commit(ctx, branch, req)
}
