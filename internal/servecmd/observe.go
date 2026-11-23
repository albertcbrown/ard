package servecmd

import (
	"context"

	"github.com/albertcbrown/ard/internal/agent"
)

// readingObserver is the ReadingRecorder of the Finisher and the worker loop:
// it records the usage windows of an agent step in the limits book, for a
// subscription (OAuth) credential only. A book or Observe error is logged as
// limits.observe_failed and never fails the job.
//
// The callers pass only agent and fix steps (never a test step, whose output
// comes from agent-written code), including a killed step with no result.
type readingObserver struct{ w *wiring }

func (o readingObserver) Observe(ctx context.Context, ws []agent.Window) {
	if o.w.Model.Auth != agent.AuthOAuthToken || len(ws) == 0 {
		return
	}
	b, err := o.w.limitsBook(ctx)
	if err == nil {
		err = b.Observe(ctx, ws)
	}
	if err != nil {
		o.w.Log.Warn("limits.observe_failed", "error", err)
	}
}
