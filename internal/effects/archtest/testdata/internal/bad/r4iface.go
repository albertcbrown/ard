package bad

import "archfix/internal/gitx"

type committer interface{ Commit() error }

var _ committer = (*gitx.Git)(nil)
