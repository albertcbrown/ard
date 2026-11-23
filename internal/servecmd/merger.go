package servecmd

import (
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/merger"
)

// mergerLoop builds the merger loop (#46). It needs no executor, Finisher or
// pool slot: it only reads GitHub, the state store and the controller's clone,
// and writes through the effect layer.
func mergerLoop(w *wiring) controller.Loop {
	return merger.New(merger.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Git: gitx.New(""),
		RepoDir: w.Root, RemoteURL: w.RemoteURL, Token: w.Token, Log: w.Log,
	})
}
