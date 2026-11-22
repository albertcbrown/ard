package committer

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/albertcbrown/ard/internal/gitx"
)

var branchRe = regexp.MustCompile(`^worker/[a-z0-9][a-z0-9-]*$`)

// Branches commits to PR branches: serialised per branch, parallel across
// branches. A lane's worktree exists only while a request runs.
type Branches struct {
	core   *core
	cfgErr error
	mu     sync.Mutex
	lanes  map[string]*lane
	closed bool
}

// NewBranches returns a per-branch committer. cfg.Branch is ignored.
func NewBranches(cfg Config, fx Effects, g *gitx.Git) *Branches {
	c, err := newCore(cfg, fx, g, false)
	return &Branches{core: c, cfgErr: err, lanes: map[string]*lane{}}
}

// laneFor returns the lane for branch, creating it on first use. Commit and
// CommitCode share it, so .ard and code commits to one branch are serialised.
func (b *Branches) laneFor(branch string) (*lane, error) {
	if b.cfgErr != nil {
		return nil, b.cfgErr
	}
	if !branchRe.MatchString(branch) {
		return nil, fmt.Errorf("%w: branch %q is not worker/<name>", ErrInvalid, branch)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	l, ok := b.lanes[branch]
	if !ok {
		wt := filepath.Join(b.core.cfg.WorkDir, "committer-branch-"+strings.ReplaceAll(branch, "/", "-"))
		l = newLane(b.core, branch, wt, false)
		b.lanes[branch] = l
	}
	return l, nil
}

// Commit commits req to branch (worker/<role>-<slug>) and pushes it. A branch
// that does not exist on the remote is ErrBranchGone; it is never created.
func (b *Branches) Commit(ctx context.Context, branch string, req Request) (Result, error) {
	l, err := b.laneFor(branch)
	if err != nil {
		return Result{}, err
	}
	return l.commit(ctx, req)
}

// Close waits for in-flight requests. Later calls return ErrClosed.
func (b *Branches) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	lanes := make([]*lane, 0, len(b.lanes))
	for _, l := range b.lanes {
		lanes = append(lanes, l)
	}
	b.mu.Unlock()
	var first error
	for _, l := range lanes {
		if err := l.close(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}
