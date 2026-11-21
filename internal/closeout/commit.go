package closeout

import (
	"context"
	"fmt"

	"github.com/albertcbrown/ard/internal/committer"
)

type adapter struct {
	main     *committer.Committer
	branches *committer.Branches
}

// NewCommitter routes a commit to the main committer when branch is "" and to
// the per-branch committer otherwise.
func NewCommitter(main *committer.Committer, branches *committer.Branches) Committer {
	return adapter{main: main, branches: branches}
}

func (a adapter) Commit(ctx context.Context, branch string, req committer.Request) (committer.Result, error) {
	if branch == "" {
		if a.main == nil {
			return committer.Result{}, fmt.Errorf("%w: no main committer", committer.ErrInvalid)
		}
		return a.main.Commit(ctx, req)
	}
	if a.branches == nil {
		return committer.Result{}, fmt.Errorf("%w: no branch committer", committer.ErrInvalid)
	}
	return a.branches.Commit(ctx, branch, req)
}
