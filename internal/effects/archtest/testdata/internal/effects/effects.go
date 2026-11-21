package effects

import (
	"archfix/internal/github"
	"archfix/internal/gitx"
)

func Do(g *gitx.Git) (github.Client, error) { _ = g.Push(); return github.New(), g.Commit() }
