package servecmd

import (
	"context"
	"fmt"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/janitor"
)

// janitorRef is where the janitor keeps its own fetch of main in the
// controller clone, apart from the committer lane's ref.
const janitorRef = "refs/ard/janitor/main"

// janitorLoop builds the janitor loop (#61). It lists .ard/ on a fresh fetch
// of main and deletes only through the shared main committer.
func janitorLoop(w *wiring) controller.Loop {
	g := gitx.New("")
	tree := func(ctx context.Context) ([]string, error) {
		if err := g.FetchRef(ctx, w.Root, w.RemoteURL, "+refs/heads/main:"+janitorRef, w.Token); err != nil {
			return nil, fmt.Errorf("fetch main: %w", err)
		}
		sha, err := g.RevParse(ctx, w.Root, janitorRef)
		if err != nil {
			return nil, err
		}
		return g.ListTree(ctx, w.Root, sha, ".ard")
	}
	return janitor.New(w.Reader, w.mainCommitter(), tree, nil)
}
