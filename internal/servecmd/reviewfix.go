package servecmd

import (
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/reviewfix"
)

// reviewfixLoop builds the review fix loop (#41). It needs no executor,
// Finisher or pool slot: it reads GitHub, the state store and the controller's
// clone, and writes labels, one comment and events.
func reviewfixLoop(w *wiring) controller.Loop {
	return reviewfix.New(reviewfix.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Git: gitx.New(""),
		RepoDir: w.Root, RemoteURL: w.RemoteURL, Token: w.Token, Log: w.Log,
	})
}
