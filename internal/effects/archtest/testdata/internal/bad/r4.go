package bad

import "archfix/internal/gitx"

func R4(g *gitx.Git) error { return g.Commit() }
